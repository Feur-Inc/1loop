// SPDX-License-Identifier: MPL-2.0

package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name, body string
	link, hard bool
	mode       int64
}

func writeTar(t *testing.T, entries []entry) string {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	for _, item := range entries {
		header := &tar.Header{Name: item.name, Mode: 0644, Typeflag: tar.TypeReg, Size: int64(len(item.body))}
		if item.mode != 0 {
			header.Mode = item.mode
		}
		switch {
		case item.link:
			header.Typeflag, header.Linkname, header.Size = tar.TypeSymlink, item.body, 0
		case item.hard:
			header.Typeflag, header.Linkname, header.Size = tar.TypeLink, item.body, 0
		case strings.HasSuffix(item.name, "/"):
			header.Typeflag, header.Mode = tar.TypeDir, 0755
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			archive.Write([]byte(item.body))
		}
	}
	archive.Close()
	zipped.Close()
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeZip(t *testing.T, entries []entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "archive.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for _, item := range entries {
		header := &zip.FileHeader{Name: item.name, Method: zip.Store}
		header.SetMode(0644)
		if item.link {
			header.SetMode(fs.ModeSymlink | 0777)
		}
		out, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		out.Write([]byte(item.body))
	}
	archive.Close()
	file.Close()
	return path
}

// unpack extracts entries as a tar.gz or a zip archive.
func unpack(t *testing.T, format string, u Unpack, entries []entry, dest string) (int64, error) {
	t.Helper()
	if format == "zip" {
		return u.Zip(writeZip(t, entries), dest)
	}
	file, err := os.Open(writeTar(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	return u.Tar(file, dest)
}

// nothingOutside fails when parent holds anything but dest's own folders.
func nothingOutside(t *testing.T, parent string) {
	t.Helper()
	filepath.WalkDir(parent, func(path string, item fs.DirEntry, err error) error {
		switch rel, _ := filepath.Rel(parent, path); rel {
		case ".", "one", filepath.Join("one", "two"):
		case filepath.Join("one", "two", "dest"):
			return filepath.SkipDir
		default:
			t.Fatalf("%s was written outside dest", rel)
		}
		return nil
	})
}

// Hostile archives are refused and write nothing outside dest (#524).
func TestUnpackRefusesHostileArchives(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	cases := map[string][]entry{
		"..":                                  {{name: "..", body: "out"}},
		"../x":                                {{name: "../escaped", body: "out"}},
		"a/../../x":                           {{name: "a/../../escaped", body: "out"}},
		"absolute":                            {{name: "/tmp/escaped", body: "out"}},
		`..\x`:                                {{name: `..\escaped`, body: "out"}},
		`a\..\..\x`:                           {{name: `a\..\..\escaped`, body: "out"}},
		"links to dest's parent, then a file": {{name: "d", body: ".", link: true}, {name: "d/x", body: "..", link: true}, {name: "d/x/escaped", body: "out"}},
		"links to dest's parent":              {{name: "d", body: ".", link: true}, {name: "d/x", body: "..", link: true}},
		// Each link's target looks inside dest from its name in the
		// archive, but every link of the chain really points one folder
		// higher than it looks: p/q/r/s/t ends three folders above dest.
		"chained links": {
			{name: "p/q/r", body: "..", link: true},
			{name: "p/q/r/s", body: "../..", link: true},
			{name: "p/q/r/s/t", body: "../../../escaped", link: true},
			{name: "p/q/r/s/t/escaped", body: "out"},
		},
		"a link through a link": {{name: "x", body: ".", link: true}, {name: "y", body: "x/..", link: true}},
		"absolute link":         {{name: "l", body: "/etc", link: true}},
		"link up":               {{name: "l", body: "../..", link: true}},
		"file under a link":     {{name: "l", body: "sub", link: true}, {name: "l/escaped", body: "out"}},
	}
	for _, format := range []string{"tar", "zip"} {
		for name, entries := range cases {
			t.Run(format+"/"+name, func(t *testing.T) {
				parent := t.TempDir()
				if _, err := unpack(t, format, Unpack{Links: true}, entries, filepath.Join(parent, "one", "two", "dest")); err == nil {
					t.Fatal("a hostile archive was extracted")
				}
				nothingOutside(t, parent)
			})
		}
	}
	t.Run("tar/hard link out", func(t *testing.T) {
		parent := t.TempDir()
		entries := []entry{{name: "h", body: "../../../escaped", hard: true}}
		if _, err := unpack(t, "tar", Unpack{HardLinks: true}, entries, filepath.Join(parent, "one", "two", "dest")); err == nil {
			t.Fatal("a hard link out of dest was copied")
		}
		nothingOutside(t, parent)
	})
}

// Without Links, links are left out and the rest extracts.
func TestUnpackLeavesLinksOut(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dest")
	_, err := unpack(t, "tar", Unpack{}, []entry{{name: "l", body: "/etc", link: true}, {name: "h", body: "f", hard: true}, {name: "f", body: "x"}}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "l")); err == nil {
		t.Fatal("a link was made")
	}
	if _, err := os.Lstat(filepath.Join(dest, "h")); err == nil {
		t.Fatal("a hard link was made")
	}
}

func TestUnpackCaps(t *testing.T) {
	big := strings.Repeat("x", 2048)
	for _, format := range []string{"tar", "zip"} {
		for name, test := range map[string]struct {
			u       Unpack
			entries []entry
			problem string
		}{
			"a file over the cap": {Unpack{MaxFile: 1024}, []entry{{name: "big", body: big}}, "big is larger"},
			"over the total":      {Unpack{MaxTotal: 3000}, []entry{{name: "a", body: big}, {name: "b", body: big}}, "the archive is larger"},
			"too many entries":    {Unpack{MaxEntries: 3}, []entry{{name: "a"}, {name: "b"}, {name: "c"}, {name: "d"}}, "more than 3 entries"},
		} {
			t.Run(format+"/"+name, func(t *testing.T) {
				_, err := unpack(t, format, test.u, test.entries, filepath.Join(t.TempDir(), "dest"))
				if err == nil || !strings.Contains(err.Error(), test.problem) {
					t.Fatalf("got %v, want %q", err, test.problem)
				}
			})
		}
	}
}

// Good archives extract as they are: names that only start with two dots,
// a macOS framework's links, prefixes dropped, rwx bits kept and nothing
// more.
func TestUnpackKeepsGoodArchives(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	for _, format := range []string{"tar", "zip"} {
		t.Run(format, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "dest")
			written, err := unpack(t, format, Unpack{Links: true}, []entry{
				{name: "F.framework/Versions/A/Resources/info.plist", body: "plist"},
				{name: "F.framework/Versions/Current", body: "A", link: true},
				{name: "F.framework/Resources", body: "Versions/Current/Resources", link: true},
				{name: "..env.example", body: "x"},
				{name: "...", body: "y"},
				{name: `win\style`, body: "z"},
			}, dest)
			if err != nil {
				t.Fatal(err)
			}
			if written != 8 {
				t.Fatalf("%d bytes written, want 8", written)
			}
			if data, err := os.ReadFile(filepath.Join(dest, "F.framework/Resources/info.plist")); err != nil || string(data) != "plist" {
				t.Fatalf("through the links: %q %v", data, err)
			}
			for _, name := range []string{"..env.example", "...", "win/style"} {
				if _, err := os.Stat(filepath.Join(dest, name)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("strip and modes", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "dest")
		executable := func(perm fs.FileMode) fs.FileMode {
			if perm&0o111 != 0 {
				return 0o700
			}
			return 0o600
		}
		_, err := unpack(t, "tar", Unpack{Strip: 1, Mode: executable, DirMode: 0o700}, []entry{
			{name: "repo-sha/"},
			{name: "repo-sha/run.sh", body: "#!/bin/sh", mode: 0o4755},
			{name: "repo-sha/plain", body: "text", mode: 0o2644},
			{name: "repo-sha/..env.example", body: "x"},
		}, dest)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]fs.FileMode{"run.sh": 0o700, "plain": 0o600, "..env.example": 0o600} {
			info, err := os.Stat(filepath.Join(dest, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode() != want {
				t.Fatalf("%s: mode %v, want %v", name, info.Mode(), want)
			}
		}
		if _, err := os.Stat(filepath.Join(dest, "repo-sha")); err == nil {
			t.Fatal("the top folder is kept")
		}
	})
	t.Run("no setuid", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "dest")
		if _, err := unpack(t, "tar", Unpack{Mode: func(perm fs.FileMode) fs.FileMode { return perm | fs.ModeSetuid | fs.ModeSetgid }}, []entry{{name: "s", body: "x", mode: 0o6755}}, dest); err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(filepath.Join(dest, "s")); info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
			t.Fatalf("mode %v", info.Mode())
		}
	})
}

// A root filesystem keeps its absolute links and copies its hard links, but
// nothing is written through an absolute link.
func TestUnpackRootFilesystem(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	rootfs := Unpack{Links: true, AbsoluteLinks: true, HardLinks: true}
	dest := filepath.Join(t.TempDir(), "dest")
	_, err := unpack(t, "tar", rootfs, []entry{
		{name: "./"},
		{name: "etc/"},
		{name: "etc/os-release", body: "debian"},
		{name: "etc/alternatives/", body: ""},
		{name: "etc/alternatives/editor", body: "/usr/bin/vi", link: true},
		{name: "usr/lib/os-release", body: "etc/os-release", hard: true},
		{name: "bin", body: "usr/bin", link: true},
	}, dest)
	if err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(filepath.Join(dest, "etc/alternatives/editor")); err != nil || target != "/usr/bin/vi" {
		t.Fatalf("absolute link: %q %v", target, err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "usr/lib/os-release")); err != nil || string(data) != "debian" {
		t.Fatalf("hard link: %q %v", data, err)
	}
	parent := t.TempDir()
	_, err = unpack(t, "tar", rootfs, []entry{
		{name: "up", body: "/", link: true},
		{name: "up/x", body: "y", link: true},
	}, filepath.Join(parent, "one", "two", "dest"))
	if err == nil {
		t.Fatal("a link was made through an absolute link")
	}
	nothingOutside(t, parent)
}

func TestUnpackCheck(t *testing.T) {
	refuse := func(name string) error {
		if strings.HasPrefix(filepath.Base(name), ".wh.") {
			return os.ErrPermission
		}
		return nil
	}
	if _, err := unpack(t, "tar", Unpack{Check: refuse}, []entry{{name: "a/.wh.b", body: "x"}}, filepath.Join(t.TempDir(), "dest")); err == nil {
		t.Fatal("Check did not refuse")
	}
}
