// SPDX-License-Identifier: MPL-2.0

package extensions

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// serveRepo answers GitHub's tarball of a repository with entries.
func serveRepo(t *testing.T, entries []tar.Header, bodies map[string]string) {
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(buffer.Bytes())
	}))
	t.Cleanup(server.Close)
	previous := githubLoad
	githubLoad = server.URL
	t.Cleanup(func() { githubLoad = previous })
}

// The top folder is dropped, executables stay executable, links are left
// out, and names that only start with two dots are kept (#524).
func TestDownloadRepoKeepsItsFiles(t *testing.T) {
	serveRepo(t, []tar.Header{
		{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "sha"}},
		{Name: "owner-repo-sha/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "owner-repo-sha/..env.example", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "owner-repo-sha/bin/run.sh", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "owner-repo-sha/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
	}, map[string]string{"owner-repo-sha/..env.example": "KEY=", "owner-repo-sha/bin/run.sh": "#!/bin/sh\n"})
	dir := filepath.Join(t.TempDir(), "staged")
	size, err := downloadRepo(context.Background(), "owner/repo", dir)
	if err != nil {
		t.Fatal(err)
	}
	if size != 14 {
		t.Fatalf("size %d, want 14", size)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "..env.example")); err != nil || string(data) != "KEY=" {
		t.Fatalf("..env.example: %q %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(dir, "bin", "run.sh")); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("run.sh: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "link")); err == nil {
		t.Fatal("a link was made")
	}
}

func TestDownloadRepoRefusesEscapes(t *testing.T) {
	serveRepo(t, []tar.Header{
		{Name: "owner-repo-sha/../../escaped", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{"owner-repo-sha/../../escaped": "out"})
	parent := t.TempDir()
	if _, err := downloadRepo(context.Background(), "owner/repo", filepath.Join(parent, "a", "staged")); err == nil {
		t.Fatal("an entry out of the staging folder was extracted")
	}
	if _, err := os.Stat(filepath.Join(parent, "escaped")); err == nil {
		t.Fatal("written outside the staging folder")
	}
}
