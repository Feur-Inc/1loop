// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

// Loopy's folder: what it keeps for as long as it lives (notes, lists,
// files the user gives it, what its scripts make). Nothing of these tools
// reaches out of it: os.Root follows no link out.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"1loop/internal/loopy"
)

// Bounds of the folder.
const (
	maxFile   = 64 << 20
	maxFolder = 2 << 30
	maxRead   = 24 << 10
)

type folder struct {
	dir  string
	root *os.Root
}

func openFolder(dir string) (*folder, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &folder{dir: dir, root: root}, nil
}

// clean is a path inside the folder, as the tools name it ("/notes/a.md").
func clean(name string) (string, error) {
	name = path.Clean("/" + strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		name = "."
	}
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", fmt.Errorf("%q is not a path in your folder", name)
	}
	return name, nil
}

func (f *folder) size() int64 {
	total := int64(0)
	fs.WalkDir(f.root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func (f *folder) list(name string, deep bool) ([]loopy.File, error) {
	name, err := clean(name)
	if err != nil {
		return nil, err
	}
	var out []loopy.File
	err = fs.WalkDir(f.root.FS(), name, func(at string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if at == name {
			return nil
		}
		info, err := d.Info()
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		out = append(out, loopy.File{Path: "/" + at, Dir: d.IsDir(), Size: info.Size(), Time: info.ModTime()})
		if len(out) >= 1000 {
			return fs.SkipAll
		}
		if d.IsDir() && !deep {
			return fs.SkipDir
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func (f *folder) open(name string) (*os.File, error) {
	name, err := clean(name)
	if err != nil {
		return nil, err
	}
	return f.root.Open(filepath.FromSlash(name))
}

func (f *folder) bytes(name string, limit int64) ([]byte, error) {
	file, err := f.open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.IsDir() {
		return nil, fmt.Errorf("%s is a folder", name)
	}
	return io.ReadAll(io.LimitReader(file, limit))
}

// read is part of a text file, from a line on.
func (f *folder) read(name string, from int) (string, error) {
	data, err := f.bytes(name, maxFile)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not text (%d bytes)", name, len(data))
	}
	lines := strings.Split(string(data), "\n")
	if from > len(lines) {
		return "", fmt.Errorf("%s has %d lines", name, len(lines))
	}
	from = max(1, from)
	text := strings.Join(lines[from-1:], "\n")
	if len(text) > maxRead {
		cut := strings.LastIndex(text[:maxRead], "\n")
		if cut <= 0 {
			cut = maxRead
		}
		kept := text[:cut]
		text = fmt.Sprintf("%s\n(…%d lines in all; read on from line %d)", strings.ToValidUTF8(kept, ""), len(lines), from+strings.Count(kept, "\n")+1)
	}
	return text, nil
}

func (f *folder) write(name string, data []byte, more bool) error {
	name, err := clean(name)
	if err != nil {
		return err
	}
	if name == "." {
		return errors.New("name a file")
	}
	if len(data) > maxFile || f.size()+int64(len(data)) > maxFolder {
		return errors.New("too large for your folder")
	}
	local := filepath.FromSlash(name)
	if dir := filepath.Dir(local); dir != "." {
		if err := f.root.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	if more {
		file, err := f.root.OpenFile(local, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = file.Write(data)
		return err
	}
	// Whole or not at all.
	temporary := local + ".1loop-tmp"
	if err := f.root.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	return f.root.Rename(temporary, local)
}

func (f *folder) edit(name, old, text string) error {
	data, err := f.bytes(name, maxFile)
	if err != nil {
		return err
	}
	switch n := strings.Count(string(data), old); {
	case old == "" || n == 0:
		return errors.New("that text is not in the file")
	case n > 1:
		return fmt.Errorf("that text is %d times in the file: give more of it", n)
	}
	return f.write(name, []byte(strings.Replace(string(data), old, text, 1)), false)
}

func (f *folder) move(from, to string) error {
	from, err := clean(from)
	if err != nil {
		return err
	}
	if to, err = clean(to); err != nil {
		return err
	}
	if from == "." || to == "." {
		return errors.New("not the whole folder")
	}
	if _, err := f.root.Lstat(filepath.FromSlash(to)); err == nil {
		return fmt.Errorf("/%s exists already", to)
	}
	if dir := path.Dir(to); dir != "." {
		if err := f.root.MkdirAll(filepath.FromSlash(dir), 0700); err != nil {
			return err
		}
	}
	return f.root.Rename(filepath.FromSlash(from), filepath.FromSlash(to))
}

// remove deletes a file or an empty folder.
func (f *folder) remove(name string) error {
	name, err := clean(name)
	if err != nil {
		return err
	}
	if name == "." {
		return errors.New("not the whole folder")
	}
	return f.root.Remove(filepath.FromSlash(name))
}

// search finds lines holding a pattern in the text files.
func (f *folder) search(pattern string) (string, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		re = regexp.MustCompile("(?i)" + regexp.QuoteMeta(pattern))
	}
	var out []string
	files, err := f.list("/", true)
	if err != nil {
		return "", err
	}
	for _, file := range files {
		if re.MatchString(file.Path) {
			out = append(out, file.Path)
		}
		if file.Dir || file.Size > 2<<20 || len(out) >= 60 {
			continue
		}
		data, err := f.bytes(file.Path, 2<<20)
		if err != nil || !utf8.Valid(data) {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if re.MatchString(line) && len(out) < 60 {
				if len(line) > 200 {
					line = strings.ToValidUTF8(line[:200], "") + "…"
				}
				out = append(out, fmt.Sprintf("%s:%d: %s", file.Path, i+1, line))
			}
		}
	}
	if len(out) == 0 {
		return "Nothing found.", nil
	}
	return strings.Join(out, "\n"), nil
}

// fresh is a name not taken in a folder, close to name.
func (f *folder) fresh(dir, name string) string {
	name = path.Base(path.Clean("/" + strings.ReplaceAll(name, "\\", "/")))
	if name == "/" || name == "." {
		name = "file"
	}
	ext := path.Ext(name)
	for i := 1; ; i++ {
		at := path.Join(dir, name)
		if i > 1 {
			at = path.Join(dir, fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), i, ext))
		}
		local, err := clean(at)
		if err != nil {
			return path.Join(dir, "file")
		}
		if _, err := f.root.Lstat(filepath.FromSlash(local)); errors.Is(err, fs.ErrNotExist) {
			return at
		}
	}
}
