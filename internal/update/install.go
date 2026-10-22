package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// running is the path of the running binary, links resolved. It is read
// before an update: once renamed to .old, the running file reports that name.
func running() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// InstallDir is the folder of the running binary, where an update goes.
func InstallDir() (string, error) {
	exe, err := running()
	if err != nil {
		return "", err
	}
	return installDir(exe), nil
}

func installDir(exe string) string {
	dir := filepath.Dir(exe)
	// 1loop-software.cmd starts the copy in software\: the Windows archive
	// unpacks in the folder above it.
	if parent := filepath.Dir(dir); filepath.Base(dir) == "software" {
		if _, err := os.Stat(filepath.Join(parent, "1loop-software.cmd")); err == nil {
			return parent
		}
	}
	return dir
}

// Install downloads this platform's file and puts it in place of the
// running binaries. Each replaced file is kept beside it as name.old.
// Archives (.tar.gz, .zip) replace the files they contain; a single binary
// replaces the running one. It returns the binary to restart: the new
// version, at the running one's path.
func Install(ctx context.Context, manifest Manifest, kind string) (string, error) {
	file, ok := manifest.Files[Platform(kind)]
	if !ok {
		return "", fmt.Errorf("No download for %s in this release", Platform(kind))
	}
	exe, err := running()
	if err != nil {
		return "", err
	}
	dir := installDir(exe)
	probe, err := os.CreateTemp(dir, ".1loop-update-*")
	if err != nil {
		return "", fmt.Errorf("Cannot write in %s: update 1loop from the release page instead", dir)
	}
	download := probe.Name()
	probe.Close()
	defer os.Remove(download)
	if err := Download(ctx, file, download); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(dir, ".1loop-update-files-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	name := path.Base(file.URL)
	switch {
	case strings.HasSuffix(name, ".tar.gz"):
		err = extractTar(download, staging)
	case strings.HasSuffix(name, ".zip"):
		err = extractZip(download, staging)
	default:
		// CreateTemp made the file 0600: the binary must be executable.
		if err = os.Chmod(download, 0755); err == nil {
			err = os.Rename(download, filepath.Join(staging, filepath.Base(exe)))
		}
	}
	if err != nil {
		return "", fmt.Errorf("Cannot unpack the update: %w", err)
	}
	return exe, replace(staging, dir)
}

// replace moves every staged file over its installed copy, keeping the
// installed one as .old (the running binary can be renamed, not written).
func replace(staging, dir string) error {
	return filepath.Walk(staging, func(source string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(staging, source)
		target := filepath.Join(dir, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if _, err := os.Stat(target); err == nil {
			os.Remove(target + ".old") // the backup of the update before this one
			if err := os.Rename(target, target+".old"); err != nil {
				return fmt.Errorf("Cannot replace %s: %w", relative, err)
			}
		}
		if err := os.Rename(source, target); err != nil {
			os.Rename(target+".old", target) // put the working copy back
			return fmt.Errorf("Cannot install %s: %w", relative, err)
		}
		return nil
	})
}

// staged resolves an archive entry inside the staging folder.
func staged(staging, name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("invalid path %q", name) // a release archive never climbs out
		}
	}
	clean := path.Clean("/" + name)[1:]
	if clean == "" || clean == "." {
		return "", fmt.Errorf("invalid path %q", name)
	}
	return filepath.Join(staging, filepath.FromSlash(clean)), nil
}

func writeEntry(target string, mode os.FileMode, from io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, from)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

func extractTar(archive, staging string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	reader := tar.NewReader(unzipped)
	for {
		entry, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.Typeflag != tar.TypeReg {
			continue // folders are made as needed; links are never followed
		}
		target, err := staged(staging, entry.Name)
		if err != nil {
			return err
		}
		if err := writeEntry(target, os.FileMode(entry.Mode).Perm(), reader); err != nil {
			return err
		}
	}
}

func extractZip(archive, staging string) error {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || !entry.Mode().IsRegular() {
			continue
		}
		target, err := staged(staging, entry.Name)
		if err != nil {
			return err
		}
		in, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeEntry(target, entry.Mode().Perm()|0700, in)
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
