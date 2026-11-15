// SPDX-License-Identifier: MPL-2.0

// Package softgl carries Mesa's OpenGL, which draws on the CPU (llvmpipe),
// inside the Windows desktop, for PCs without an OpenGL driver: a virtual
// machine, some remote desktops. 1loop.exe does not link opengl32.dll
// (third_party/go-gl, third_party/glfw), so before GLFW starts, 1loop can
// load Mesa's from its own folder in the user's local data
// (%LOCALAPPDATA%\1loop\mesa-VERSION), wherever the exe is, Program Files
// included. Nothing is installed on the system, nothing is put beside the
// exe. internal/ui (software_gl_windows.go) decides when.
//
// The DLLs come from the pinned mesa-dist-win release, xz-compressed into
// this package by `go generate ./internal/softgl` (fetch/).
package softgl

// On any system, for the Windows build (GOOS unset: fetch runs here).
//go:generate go run ./fetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ulikunitz/xz"
)

// Version is the Mesa release carried (fetch/main.go pins its archive).
const Version = "26.2.4"

// marker, in Dir, says what was extracted there: the embedded files' sum.
const marker = "1loop-mesa.txt"

// Available says this build carries Mesa: the Windows desktop does.
func Available() bool { return len(files()) > 0 }

// Dir is where Mesa is extracted: 1loop's folder in the user's local data.
func Dir() (string, error) {
	base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "1loop", "mesa-"+Version), nil
}

// sum identifies the embedded files, so that an extraction from another
// build of the same version is written again.
func sum() string {
	hash := sha256.New()
	for _, name := range names() {
		hash.Write([]byte(name))
		hash.Write(files()[name])
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// Extracted says Mesa is complete in dir, from this build.
func Extracted(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, marker))
	if err != nil || string(bytes.TrimSpace(data)) != sum() {
		return false
	}
	for _, name := range names() {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// Extract writes Mesa and its licences in Dir, unless they already are
// there, each file through a temporary one, the marker last. It returns Dir.
func Extract() (string, error) {
	if !Available() {
		return "", errors.New("this build of 1loop does not carry Mesa")
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if Extracted(dir) {
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "licenses"), 0o755); err != nil {
		return "", err
	}
	for _, name := range names() {
		reader, err := xz.NewReader(bytes.NewReader(files()[name]))
		if err != nil {
			return "", err
		}
		if err := write(filepath.Join(dir, name), reader); err != nil {
			return "", err
		}
	}
	entries, _ := fs.ReadDir(licenses, "licenses")
	for _, entry := range entries {
		data, err := licenses.ReadFile("licenses/" + entry.Name())
		if err == nil {
			err = write(filepath.Join(dir, "licenses", entry.Name()), bytes.NewReader(data))
		}
		if err != nil {
			return "", err
		}
	}
	return dir, write(filepath.Join(dir, marker), bytes.NewReader([]byte(sum()+"\r\n")))
}

// write puts what reader holds in path, through a temporary file. A file
// another 1loop has loaded cannot be replaced: when it is already there,
// whole, it is kept.
func write(path string, reader io.Reader) error {
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	_, err = io.Copy(file, reader)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		if err = os.Rename(temporary, path); err != nil && same(temporary, path) {
			err = nil
		}
	}
	if err != nil || fileExists(temporary) {
		os.Remove(temporary)
	}
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// same says two files hold the same bytes.
func same(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

// names are the DLLs, gallium first: opengl32.dll loads it.
func names() []string {
	if !Available() {
		return nil
	}
	return []string{"libgallium_wgl.dll", "opengl32.dll"}
}
