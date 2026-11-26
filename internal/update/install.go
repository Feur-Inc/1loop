// SPDX-License-Identifier: MPL-2.0

package update

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"1loop/internal/core"
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

func installDir(exe string) string {
	dir := filepath.Dir(exe)
	// 1loop-software.cmd starts the copy in software\: the Windows archive
	// of releases before #431 unpacked in the folder above it.
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
	if Packaged != "" {
		return "", fmt.Errorf("%s", packagedProblem())
	}
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
		// A single binary replaces the running one where it is, even the
		// software\ copy of the former Windows archive (installDir): its
		// restart is this path.
		dir = filepath.Dir(exe)
	}
	if err != nil {
		return "", fmt.Errorf("Cannot unpack the update: %w", err)
	}
	return exe, replace(staging, dir)
}

// replace moves every staged file over its installed copy, keeping the
// installed one as .old (the running binary can be renamed, not written).
// When one cannot be, those already replaced go back too: an installation
// never mixes two versions (#394).
func replace(staging, dir string) error {
	type change struct {
		target string
		kept   bool // the installed copy is in .old
	}
	var done []change
	err := filepath.Walk(staging, func(source string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relative, _ := filepath.Rel(staging, source)
		target := filepath.Join(dir, relative)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		kept := false
		if _, err := os.Stat(target); err == nil {
			os.Remove(target + ".old") // the backup of the update before this one
			if err := os.Rename(target, target+".old"); err != nil {
				return fmt.Errorf("Cannot replace %s: %w", relative, err)
			}
			kept = true
		}
		done = append(done, change{target, kept})
		if err := os.Rename(source, target); err != nil {
			return fmt.Errorf("Cannot install %s: %w", relative, err)
		}
		return nil
	})
	if err == nil {
		return nil
	}
	var stuck []string
	for i := len(done) - 1; i >= 0; i-- {
		back := done[i]
		var failed error
		if back.kept {
			os.Remove(back.target) // Windows does not rename over a file
			failed = os.Rename(back.target+".old", back.target)
		} else if failed = os.Remove(back.target); os.IsNotExist(failed) {
			failed = nil
		}
		if failed != nil {
			stuck = append(stuck, filepath.Base(back.target))
		}
	}
	if len(stuck) > 0 {
		return fmt.Errorf("%w; and the version before could not be put back for %s: install 1loop again", err, strings.Join(stuck, ", "))
	}
	return err
}

// A release archive holds regular files only: links and anything outside
// staging are refused or left out by core.Unpack (#524).
var releaseArchive = core.Unpack{MaxTotal: 4 << 30}

func extractTar(archive, staging string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = releaseArchive.Tar(file, staging)
	return err
}

func extractZip(archive, staging string) error {
	unpack := releaseArchive
	unpack.Mode = func(perm os.FileMode) os.FileMode { return perm | 0700 }
	_, err := unpack.Zip(archive, staging)
	return err
}
