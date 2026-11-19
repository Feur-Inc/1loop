// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"archive/zip"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type entry struct {
	name, body string
	link       bool
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

// Hostile archives write nothing outside dest (#524).
func TestUnzipRefusesHostileArchives(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	for name, entries := range map[string][]entry{
		"links to dest's parent":     {{name: "d", body: ".", link: true}, {name: "d/x", body: "..", link: true}, {name: "d/x/escaped", body: "out"}},
		"chained links, then a file": {{name: "a", body: ".", link: true}, {name: "a/b", body: "..", link: true}},
		// Each link's target looks inside dest from its name in the archive,
		// but every link in the chain really points one folder higher than
		// it looks: p/q/r/s/t ends three folders above dest.
		"chained links": {
			{name: "p/q/r", body: "..", link: true},
			{name: "p/q/r/s", body: "../..", link: true},
			{name: "p/q/r/s/t", body: "../../../escaped", link: true},
			{name: "p/q/r/s/t/escaped", body: "out"},
		},
		"parent":            {{name: "../escaped", body: "out"}},
		"absolute":          {{name: "/tmp/escaped", body: "out"}},
		"absolute link":     {{name: "l", body: "/etc", link: true}},
		"link up":           {{name: "l", body: "../..", link: true}},
		"file under a link": {{name: "l", body: "sub", link: true}, {name: "l/escaped", body: "out"}},
	} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "one", "two", "dest")
			if err := unzip(writeZip(t, entries), dest); err == nil {
				t.Fatal("a hostile archive was extracted")
			}
			// Nothing but dest's own folders may exist in parent afterwards.
			filepath.WalkDir(parent, func(path string, entry fs.DirEntry, err error) error {
				switch rel, _ := filepath.Rel(parent, path); rel {
				case ".", "one", filepath.Join("one", "two"):
					return nil
				case filepath.Join("one", "two", "dest"):
					return filepath.SkipDir
				default:
					t.Fatalf("%s was written outside dest", rel)
				}
				return nil
			})
		})
	}
}

// What Chromium's archives hold still extracts: a macOS framework's links,
// and names that only start with two dots.
func TestUnzipKeepsLegitimateLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	dest := filepath.Join(t.TempDir(), "dest")
	err := unzip(writeZip(t, []entry{
		{name: "F.framework/Versions/A/Resources/info.plist", body: "plist"},
		{name: "F.framework/Versions/Current", body: "A", link: true},
		{name: "F.framework/Resources", body: "Versions/Current/Resources", link: true},
		{name: "..env.example", body: "x"},
	}), dest)
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "F.framework/Resources/info.plist")); err != nil || string(data) != "plist" {
		t.Fatalf("through the links: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "..env.example")); err != nil {
		t.Fatal(err)
	}
}
