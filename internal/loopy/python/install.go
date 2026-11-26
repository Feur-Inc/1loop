// SPDX-License-Identifier: MPL-2.0

//go:build !js

// Package python is the Python Loopy's AutoLinks run with: 1loop's own,
// downloaded the first time (python-build-standalone, checked against the
// sums below), the same on Linux, Windows, macOS and the phone's engine.
// Where it cannot run (NixOS, other processors), the system's python3.
package python

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	version = "3.14.10"
	build   = "20261124"
	certifi = "certifi==2026.11.20"
	origin  = "https://github.com/astral-sh/python-build-standalone/releases/download/" + build + "/"
)

// archives are the builds, by GOOS/GOARCH.
var archives = map[string]struct{ target, sum string }{
	"linux/amd64":   {"x86_64-unknown-linux-gnu", "73ee0629c19e9f87077f318cd50d7e703137c274479ce8e8fd5723e75c86c9bc"},
	"linux/arm64":   {"aarch64-unknown-linux-gnu", "2f5df923b202f9054b665c4b4c6ee3fc08bf1ec46cc640bb68ec52aecc658d30"},
	"windows/amd64": {"x86_64-pc-windows-msvc", "72d45d2d16c509293999550c684003b0411ab9ec6e473675d1df2af5be050790"},
	"windows/arm64": {"aarch64-pc-windows-msvc", "ca7446cb0a7c65782bffecc75f02bc305088fd5ab59c47075d06359372371a68"},
	"darwin/amd64":  {"x86_64-apple-darwin", "8198777467d9d3900417dc1cfd9d512956deaebabe293d08aea5cfa5c70d6957"},
	"darwin/arm64":  {"aarch64-apple-darwin", "81fca81fcc8a715288fd12286b66bbdaaf4a0db6818bc91826ddbcffae2ac2cf"},
}

// Python is an interpreter kept in a folder; it is made at the first use.
type Python struct {
	dir string
	mu  sync.Mutex
	exe string
	ca  string
}

// New is a Python kept in dir.
func New(dir string) *Python { return &Python{dir: dir} }

func executable(root string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(root, "python.exe")
	}
	return filepath.Join(root, "bin", "python3")
}

// Ready says whether it needs nothing downloaded.
func (p *Python) Ready() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exe != "" || fileExists(executable(filepath.Join(p.dir, version+"+"+build)))
}

// ensure finds the interpreter, or downloads it; progress is told how far.
func (p *Python) ensure(ctx context.Context, progress func(string)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exe != "" {
		return nil
	}
	root := filepath.Join(p.dir, version+"+"+build)
	exe := executable(root)
	archive, known := archives[runtime.GOOS+"/"+runtime.GOARCH]
	if known && !fileExists(exe) && !fileExists("/etc/NIXOS") {
		if err := p.download(ctx, archive.target, archive.sum, root, progress); err != nil {
			return fmt.Errorf("Python could not be downloaded: %w", err)
		}
	}
	if known && fileExists(exe) && starts(ctx, exe) {
		p.ca = filepath.Join(root, "cacert.pem")
		if !fileExists(p.ca) {
			p.certificates(ctx, exe, root)
		}
		if !fileExists(p.ca) {
			p.ca = ""
		}
		p.exe = exe
		return nil
	}
	for _, name := range []string{"python3", "python"} {
		if found, err := exec.LookPath(name); err == nil && starts(ctx, found) {
			p.exe = found
			return nil
		}
	}
	return errors.New("no Python runs here: install python3 (3.9 or newer) where Loopy lives")
}

func starts(ctx context.Context, exe string) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-I", "-c", "import sys, ssl; sys.exit(sys.version_info < (3, 9))")
	hidden(cmd)
	return cmd.Run() == nil
}

// certificates keeps Mozilla's list of authorities: a bare system (the
// phone's, a slim image) may have none where this Python looks.
func (p *Python) certificates(ctx context.Context, exe, root string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	into, err := os.MkdirTemp(p.dir, ".certifi-")
	if err != nil {
		return
	}
	defer os.RemoveAll(into)
	cmd := exec.CommandContext(ctx, exe, "-I", "-m", "pip", "install", "--quiet", "--disable-pip-version-check", "--no-input", "--no-deps", "--only-binary", ":all:", "--target", into, certifi)
	cmd.Env = environment(into)
	hidden(cmd)
	if cmd.Run() != nil {
		return
	}
	if data, err := os.ReadFile(filepath.Join(into, "certifi", "cacert.pem")); err == nil {
		os.WriteFile(filepath.Join(root, "cacert.pem"), data, 0600)
	}
}

func (p *Python) download(ctx context.Context, target, sum, root string, progress func(string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return err
	}
	work, err := os.MkdirTemp(p.dir, ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	address := origin + "cpython-" + version + "%2B" + build + "-" + target + "-install_only_stripped.tar.gz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 15 * time.Minute}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", request.URL.Host, response.Status)
	}
	file, err := os.Create(filepath.Join(work, "python.tar.gz"))
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	total, done, said := max(response.ContentLength, 1), int64(0), int64(-1)
	buffer := make([]byte, 256<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err := file.Write(buffer[:n]); err != nil {
				return err
			}
			hash.Write(buffer[:n])
			done += int64(n)
			if done > 200<<20 {
				return errors.New("the download is too large")
			}
			if percent := done * 100 / total; percent/10 != said/10 {
				said = percent
				progress(fmt.Sprintf("Getting Python (%d%% of %d MB)", percent, total>>20))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != sum {
		return errors.New("the download is not the expected one (checksum)")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	progress("Unpacking Python")
	out := filepath.Join(work, "out")
	if err := untar(file, out); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(out, "python"), root); err != nil && !fileExists(executable(root)) {
		return err
	}
	return nil
}

// untar unpacks in dest; nothing lands outside it: names are local, and
// links, made last, point inside.
func untar(from io.Reader, dest string) error {
	zipped, err := gzip.NewReader(from)
	if err != nil {
		return err
	}
	reader := tar.NewReader(zipped)
	links := map[string]string{}
	var order []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimSuffix(header.Name, "/"))
		if !filepath.IsLocal(name) {
			return fmt.Errorf("%q is outside the archive", header.Name)
		}
		target := filepath.Join(dest, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0700|0600)
			if err != nil {
				return err
			}
			_, err = io.Copy(file, reader)
			if closed := file.Close(); err == nil {
				err = closed
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			points := filepath.FromSlash(header.Linkname)
			inside := points
			if header.Typeflag == tar.TypeSymlink {
				inside = filepath.Join(filepath.Dir(name), points)
			}
			if filepath.IsAbs(points) || !filepath.IsLocal(inside) {
				return fmt.Errorf("%q links outside the archive", header.Name)
			}
			links[target] = filepath.Join(dest, inside)
			order = append(order, target)
		}
	}
	for _, target := range order {
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(target), links[target])
		if err != nil {
			return err
		}
		if err := os.Symlink(relative, target); err != nil {
			if runtime.GOOS == "windows" {
				continue // its archive holds none that matters
			}
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// With is the same Python with an interpreter that is there already.
func (p *Python) With(exe string) *Python { return &Python{dir: p.dir, exe: exe} }
