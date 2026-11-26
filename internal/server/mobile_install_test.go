// SPDX-License-Identifier: MPL-2.0

package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func debianLayer(t *testing.T, entries []tar.Header, bodies map[string]string) *bytes.Reader {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	for _, header := range entries {
		header.Size = int64(len(bodies[header.Name]))
		if err := archive.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		archive.Write([]byte(bodies[header.Name]))
	}
	archive.Close()
	zipped.Close()
	return bytes.NewReader(buffer.Bytes())
}

// The Debian root keeps its absolute links and its hard links (as copies).
func TestExtractMobileDebian(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	root := filepath.Join(t.TempDir(), "debian")
	err := extractMobileDebian(root, debianLayer(t, []tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "usr/bin/vi", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "usr/bin/view", Typeflag: tar.TypeLink, Linkname: "usr/bin/vi"},
		{Name: "etc/alternatives/editor", Typeflag: tar.TypeSymlink, Linkname: "/usr/bin/vi"},
		{Name: "bin", Typeflag: tar.TypeSymlink, Linkname: "usr/bin"},
	}, map[string]string{"usr/bin/vi": "vi"}))
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "bin", "view")); err != nil || string(data) != "vi" {
		t.Fatalf("hard link through bin: %q %v", data, err)
	}
	if target, err := os.Readlink(filepath.Join(root, "etc/alternatives/editor")); err != nil || target != "/usr/bin/vi" {
		t.Fatalf("absolute link: %q %v", target, err)
	}
}

// A whiteout or a link through an absolute link is refused, and the root
// removed.
func TestExtractMobileDebianRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	for name, entries := range map[string][]tar.Header{
		"whiteout":                 {{Name: "etc/.wh.passwd", Typeflag: tar.TypeReg, Mode: 0o644}},
		"through an absolute link": {{Name: "up", Typeflag: tar.TypeSymlink, Linkname: "/"}, {Name: "up/x", Typeflag: tar.TypeSymlink, Linkname: "y"}},
		"parent":                   {{Name: "../escaped", Typeflag: tar.TypeReg, Mode: 0o644}},
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "debian")
			if err := extractMobileDebian(root, debianLayer(t, entries, nil)); err == nil {
				t.Fatal("extracted")
			}
			if _, err := os.Stat(root); err == nil {
				t.Fatal("the root is kept after a failure")
			}
		})
	}
}
