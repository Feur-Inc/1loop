// SPDX-License-Identifier: MPL-2.0

// Command fetch writes the Mesa DLLs that internal/softgl embeds, from the
// pinned mesa-dist-win release: run by `go generate ./internal/softgl`,
// which packaging/windows/build.sh and `make windows` do. It downloads the
// archive once into the user's cache, checks its sum, takes the two x64
// DLLs out of it with 7-Zip (7z, 7za or 7zz) and compresses them with xz,
// also kept in the cache: the next runs only copy them.
//
//	go run ./internal/softgl/fetch [-o DIR]
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ulikunitz/xz"
)

const (
	version = "26.2.4"
	archive = "https://github.com/pal1000/mesa-dist-win/releases/download/" + version + "/mesa3d-" + version + "-release-msvc.7z"
	digest  = "351fc8c8b695878ffb3eaa044b3ead08672a48b1a045e3c3e3975811df0f6695"
)

var dlls = []string{"opengl32.dll", "libgallium_wgl.dll"}

func main() {
	out := flag.String("o", ".", "the folder to write NAME.dll.xz in (internal/softgl)")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "softgl fetch:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	base, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	cache := filepath.Join(base, "1loop-windows-build")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	compressed := filepath.Join(cache, "mesa3d-"+version+"-"+digest[:12])
	if !complete(compressed) {
		if err := prepare(cache, compressed); err != nil {
			return err
		}
	}
	for _, name := range dlls {
		data, err := os.ReadFile(filepath.Join(compressed, name+".xz"))
		if err != nil {
			return err
		}
		if old, err := os.ReadFile(filepath.Join(out, name+".xz")); err == nil && bytes.Equal(old, data) {
			continue
		}
		if err := os.WriteFile(filepath.Join(out, name+".xz"), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func complete(dir string) bool {
	for _, name := range dlls {
		if _, err := os.Stat(filepath.Join(dir, name+".xz")); err != nil {
			return false
		}
	}
	return true
}

// prepare downloads and checks the archive, extracts the DLLs and writes
// them xz-compressed in compressed.
func prepare(cache, compressed string) error {
	path := filepath.Join(cache, filepath.Base(archive))
	if checked(path) != nil {
		fmt.Fprintln(os.Stderr, "softgl fetch: downloading", archive)
		if err := download(path); err != nil {
			return err
		}
	}
	if err := checked(path); err != nil {
		return err
	}
	work, err := os.MkdirTemp(cache, "mesa-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := extract(path, work); err != nil {
		return err
	}
	staging := compressed + ".tmp"
	os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	for _, name := range dlls {
		fmt.Fprintln(os.Stderr, "softgl fetch: compressing", name)
		if err := compress(filepath.Join(work, name), filepath.Join(staging, name+".xz")); err != nil {
			return err
		}
	}
	os.RemoveAll(compressed)
	return os.Rename(staging, compressed)
}

func checked(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != digest {
		return fmt.Errorf("%s: sha256 %s, want %s", path, got, digest)
	}
	return nil
}

func download(path string) error {
	response, err := http.Get(archive)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", archive, response.StatusCode)
	}
	file, err := os.Create(path + ".tmp")
	if err != nil {
		return err
	}
	_, err = io.Copy(file, response.Body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// extract takes the x64 DLLs out of the archive with 7-Zip.
func extract(path, dir string) error {
	var tool string
	for _, name := range []string{"7z", "7za", "7zz"} {
		if found, err := exec.LookPath(name); err == nil {
			tool = found
			break
		}
	}
	if tool == "" {
		return fmt.Errorf("7-Zip (7z) is needed to take Mesa out of %s: install p7zip-full (Debian, Ubuntu) or 7-Zip", filepath.Base(path))
	}
	args := []string{"e", path, "-o" + dir, "-y"}
	for _, name := range dlls {
		args = append(args, "x64/"+name)
	}
	command := exec.Command(tool, args...)
	command.Stderr = os.Stderr
	if output, err := command.Output(); err != nil {
		return fmt.Errorf("%s: %v\n%s", tool, err, output)
	}
	for _, name := range dlls {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("x64/%s is not in %s", name, filepath.Base(path))
		}
	}
	return nil
}

// compress writes source xz-compressed: with xz itself where it is
// installed (-9e, one thread so the output is always the same: 16 MB for
// gallium), otherwise in Go (20 MB).
func compress(source, target string) error {
	if tool, err := exec.LookPath("xz"); err == nil {
		output, err := os.Create(target)
		if err != nil {
			return err
		}
		command := exec.Command(tool, "-9e", "-T1", "-c", source)
		command.Stdout, command.Stderr = output, os.Stderr
		err = command.Run()
		if closeErr := output.Close(); err == nil {
			err = closeErr
		}
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	writer, err := xz.WriterConfig{DictCap: 64 << 20}.NewWriter(output)
	if err == nil {
		_, err = io.Copy(writer, input)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	return err
}
