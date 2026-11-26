// SPDX-License-Identifier: MPL-2.0

package core

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Unpack extracts tar (gzipped or not) and zip archives so that nothing
// lands outside the destination (#524):
//   - a name must be local once "\" is read as "/": no "..", not absolute,
//     while "..env.example" is fine;
//   - files and folders are written first, while no link of the archive
//     exists, and never through a link already there;
//   - links come after, each one's folder resolved on disk and its target
//     resolved from there, then all of them against the real filesystem; so
//     a chain such as "d -> ." then "d/x -> .." cannot reach dest's parent;
//   - sizes and the number of entries are capped;
//   - permissions keep the rwx bits only: no setuid, setgid or sticky bit.
//
// The zero value keeps regular files and folders, leaves links out, and
// refuses devices and pipes.
type Unpack struct {
	Strip int                     // leading folders dropped from each name; an entry with nothing left is skipped
	Check func(name string) error // refuses a name (after Strip); optional

	Mode    func(perm fs.FileMode) fs.FileMode // a file's permissions from the archive's; perm|0600 by default
	DirMode fs.FileMode                        // 0755 by default

	Links         bool // symbolic links are made, inside dest only
	AbsoluteLinks bool // a root filesystem: links may also name absolute paths, which are never followed here
	HardLinks     bool // tar hard links are written as copies of their file

	MaxTotal   int64 // bytes written in all; 1 GB by default
	MaxFile    int64 // bytes of one file; MaxTotal by default
	MaxEntries int   // 100 000 by default
}

type archiveLink struct{ name, target string }

type unpacking struct {
	Unpack
	root                string
	entries             int
	total               int64
	symlinks, hardlinks []archiveLink
}

// Tar extracts a tar stream, gzipped or not, into dest and returns the
// bytes written.
func (u Unpack) Tar(r io.Reader, dest string) (int64, error) {
	stream := bufio.NewReader(r)
	var from io.Reader = stream
	if magic, _ := stream.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		unzipped, err := gzip.NewReader(stream)
		if err != nil {
			return 0, err
		}
		defer unzipped.Close()
		from = unzipped
	}
	run, err := u.start(dest)
	if err != nil {
		return 0, err
	}
	archive := tar.NewReader(from)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return run.total, err
		}
		if err := run.count(); err != nil {
			return run.total, err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name, keep, err := run.name(header.Name)
		if err != nil {
			return run.total, err
		}
		if !keep {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			err = run.mkdirs(name)
		case tar.TypeReg: // the reader reports old regular entries (TypeRegA) as TypeReg
			err = run.write(name, archive, header.FileInfo().Mode().Perm())
		case tar.TypeSymlink:
			if run.Links {
				run.symlinks = append(run.symlinks, archiveLink{name, header.Linkname})
			}
		case tar.TypeLink:
			if run.HardLinks {
				var target string
				if target, keep, err = run.name(header.Linkname); err == nil && !keep {
					err = fmt.Errorf("the archive holds an unsafe link: %s", header.Name)
				}
				run.hardlinks = append(run.hardlinks, archiveLink{name, target})
			}
		default:
			err = fmt.Errorf("the archive holds a special file: %s", header.Name)
		}
		if err != nil {
			return run.total, err
		}
	}
	return run.total, run.finish()
}

// Zip extracts the zip archive at path into dest and returns the bytes
// written.
func (u Unpack) Zip(path, dest string) (int64, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	run, err := u.start(dest)
	if err != nil {
		return 0, err
	}
	for _, file := range reader.File {
		if err := run.count(); err != nil {
			return run.total, err
		}
		name, keep, err := run.name(file.Name)
		if err != nil {
			return run.total, err
		}
		if !keep {
			continue
		}
		mode := file.Mode()
		switch {
		case mode.IsDir():
			err = run.mkdirs(name)
		case mode&fs.ModeSymlink != 0:
			if run.Links {
				var target []byte
				if target, err = readZipped(file, 4096); err == nil {
					run.symlinks = append(run.symlinks, archiveLink{name, string(target)})
				}
			}
		case mode.IsRegular():
			var in io.ReadCloser
			if in, err = file.Open(); err == nil {
				err = run.write(name, in, mode.Perm())
				in.Close()
			}
		default:
			err = fmt.Errorf("the archive holds a special file: %s", file.Name)
		}
		if err != nil {
			return run.total, err
		}
	}
	return run.total, run.finish()
}

func (u Unpack) start(dest string) (*unpacking, error) {
	if u.Mode == nil {
		u.Mode = func(perm fs.FileMode) fs.FileMode { return perm | 0600 }
	}
	if u.DirMode == 0 {
		u.DirMode = 0755
	}
	if u.MaxTotal <= 0 {
		u.MaxTotal = 1 << 30
	}
	if u.MaxFile <= 0 || u.MaxFile > u.MaxTotal {
		u.MaxFile = u.MaxTotal
	}
	if u.MaxEntries <= 0 {
		u.MaxEntries = 100_000
	}
	if err := os.MkdirAll(dest, u.DirMode); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return nil, err
	}
	return &unpacking{Unpack: u, root: root}, nil
}

func (u *unpacking) count() error {
	u.entries++
	if u.entries > u.MaxEntries {
		return fmt.Errorf("the archive holds more than %d entries", u.MaxEntries)
	}
	return nil
}

// name is an entry's name, cleaned, slash-separated and relative to dest;
// false when Strip leaves nothing of it.
func (u *unpacking) name(raw string) (string, bool, error) {
	name := strings.ReplaceAll(raw, `\`, "/")
	clean := path.Clean(name)
	if path.IsAbs(name) || clean != "." && !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", false, fmt.Errorf("the archive holds an unsafe path: %s", raw)
	}
	parts := strings.Split(clean, "/")
	if clean == "." || len(parts) <= u.Strip {
		return "", false, nil
	}
	clean = strings.Join(parts[u.Strip:], "/")
	if u.Check != nil {
		if err := u.Check(clean); err != nil {
			return "", false, err
		}
	}
	return clean, true, nil
}

// mkdirs makes the folders of name in dest, refusing any link on the way.
func (u *unpacking) mkdirs(name string) error {
	if name == "." {
		return nil
	}
	dir := u.root
	for _, part := range strings.Split(name, "/") {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		switch {
		case os.IsNotExist(err):
			if err := os.Mkdir(dir, u.DirMode); err != nil {
				return err
			}
		case err != nil:
			return err
		case !info.IsDir():
			return fmt.Errorf("the archive writes through a link or a file: %s", name)
		}
	}
	return nil
}

func (u *unpacking) write(name string, from io.Reader, perm fs.FileMode) error {
	if err := u.mkdirs(path.Dir(name)); err != nil {
		return err
	}
	target := filepath.Join(u.root, filepath.FromSlash(name))
	if info, err := os.Lstat(target); err == nil {
		if info.IsDir() {
			return fmt.Errorf("the archive holds a folder and a file named %s", name)
		}
		os.Remove(target) // an earlier entry of the same name: never written through
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, u.Mode(perm.Perm()).Perm())
	if err != nil {
		return err
	}
	limit := min(u.MaxFile, u.MaxTotal-u.total)
	written, err := io.Copy(out, io.LimitReader(from, limit+1))
	closeErr := out.Close()
	u.total += written
	switch {
	case err != nil:
		return err
	case written > u.MaxFile:
		return fmt.Errorf("%s is larger than %d MB", name, u.MaxFile>>20)
	case written > limit:
		return fmt.Errorf("the archive is larger than %d MB", u.MaxTotal>>20)
	}
	return closeErr
}

// finish copies the hard links' files, then makes the symbolic links.
func (u *unpacking) finish() error {
	for _, link := range u.hardlinks {
		source := filepath.Join(u.root, filepath.FromSlash(link.target))
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("the archive's hard link %s names no file", link.name)
		}
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		err = u.write(link.name, in, info.Mode().Perm())
		in.Close()
		if err != nil {
			return err
		}
	}
	for _, link := range u.symlinks {
		// The folder is resolved on disk, through the links made before
		// this one: the target is read from where the link really is.
		parent, err := NewPath(u.root, filepath.Join(u.root, filepath.FromSlash(path.Dir(link.name))))
		if err != nil || !Within(u.root, parent) {
			return fmt.Errorf("the archive holds an unsafe link: %s", link.name)
		}
		if filepath.IsAbs(link.target) || path.IsAbs(link.target) {
			if !u.AbsoluteLinks {
				return fmt.Errorf("the archive holds an unsafe link: %s", link.name)
			}
		} else if !Within(u.root, filepath.Join(parent, filepath.FromSlash(link.target))) {
			return fmt.Errorf("the archive holds an unsafe link: %s", link.name)
		}
		if err := os.MkdirAll(parent, u.DirMode); err != nil { // its missing folders hold no link
			return err
		}
		if err := os.Symlink(link.target, filepath.Join(parent, path.Base(link.name))); err != nil {
			return err // a file or folder of the archive is already there
		}
	}
	if u.AbsoluteLinks {
		return nil // a root filesystem's links are read from its own root, not this system's
	}
	for _, link := range u.symlinks {
		resolved, err := filepath.EvalSymlinks(filepath.Join(u.root, filepath.FromSlash(link.name)))
		if err == nil && !Within(u.root, resolved) {
			return fmt.Errorf("the archive holds an unsafe link: %s", link.name)
		}
	}
	return nil
}

func readZipped(file *zip.File, limit int64) ([]byte, error) {
	in, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer in.Close()
	return io.ReadAll(io.LimitReader(in, limit))
}
