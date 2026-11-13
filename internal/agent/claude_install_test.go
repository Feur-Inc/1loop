// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
)

// releaseServer serves a Claude Code release as downloads.claude.ai does,
// its manifest signed by a test key that stands in for Anthropic's.
func releaseServer(t *testing.T, binary []byte, checksum string, signer *openpgp.Entity) {
	t.Helper()
	if claudePlatform() == "" || runtime.GOOS == "windows" {
		t.Skip("no Claude Code build for this system")
	}
	if checksum == "" {
		sum := sha256.Sum256(binary)
		checksum = hex.EncodeToString(sum[:])
	}
	manifest := []byte(fmt.Sprintf(`{"version":"9.9.9","platforms":{%q:{"binary":"claude","checksum":%q,"size":%d}}}`, claudePlatform(), checksum, len(binary)))
	var signature bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&signature, signer, bytes.NewReader(manifest), nil); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"/latest":                                []byte("9.9.9\n"),
		"/9.9.9/manifest.json":                   manifest,
		"/9.9.9/manifest.json.sig":               signature.Bytes(),
		"/9.9.9/" + claudePlatform() + "/claude": binary,
	}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(web.Close)
	saved := claudeDownloads
	claudeDownloads = web.URL
	t.Cleanup(func() { claudeDownloads = saved })
}

// trustKey makes entity the pinned release key for the test.
func trustKey(t *testing.T, entity *openpgp.Entity) {
	t.Helper()
	var armored bytes.Buffer
	writer, err := armor.Encode(&armored, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.Serialize(writer); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	savedKey, savedFingerprint := claudeReleaseKey, claudeReleaseFingerprint
	claudeReleaseKey, claudeReleaseFingerprint = armored.String(), strings.ToUpper(hex.EncodeToString(entity.PrimaryKey.Fingerprint[:]))
	t.Cleanup(func() { claudeReleaseKey, claudeReleaseFingerprint = savedKey, savedFingerprint })
}

func testKey(t *testing.T) *openpgp.Entity {
	t.Helper()
	entity, err := openpgp.NewEntity("test", "", "test@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	return entity
}

// The server's Install Claude Code puts the claude command in its data
// folder (ClaudeInstallDir), where ClaudeExecutable finds it with no
// claude on PATH or in the home folder: the slim image's case.
func TestInstallClaudeCodeIn(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ONELOOP_FLATPAK", "")
	saved := ClaudeInstallDir
	ClaudeInstallDir = filepath.Join(t.TempDir(), "claude-code")
	t.Cleanup(func() { ClaudeInstallDir = saved })
	if _, err := ClaudeExecutable(); err == nil {
		t.Fatal("no claude yet")
	}
	key := testKey(t)
	trustKey(t, key)
	releaseServer(t, []byte("#!/bin/sh\necho '9.9.9 (Claude Code)'\n"), "", key)
	var said []string
	path, err := InstallClaudeCodeIn(context.Background(), ClaudeInstallDir, func(text string) { said = append(said, text) })
	if err != nil || path != filepath.Join(ClaudeInstallDir, "claude") || len(said) == 0 {
		t.Fatalf("install: %q %v %q", path, err, said)
	}
	if found, err := ClaudeExecutable(); err != nil || found != path {
		t.Fatalf("ClaudeExecutable: %q %v, want %q", found, err, path)
	}
	if entries, _ := os.ReadDir(ClaudeInstallDir); len(entries) != 1 {
		t.Fatalf("only claude stays: %d files", len(entries))
	}
}

// Nothing unverified is kept, nor run: a release signed by another key, a
// binary whose SHA-256 is not the manifest's, one that does not start.
func TestInstallClaudeCodeInRefuses(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("ONELOOP_FLATPAK", "")
	trusted := testKey(t)
	trustKey(t, trusted)
	marker := filepath.Join(t.TempDir(), "ran")
	script := []byte("#!/bin/sh\ntouch " + marker + "\n")
	for name, setup := range map[string]func(){
		"signature": func() { releaseServer(t, script, "", testKey(t)) },
		"checksum":  func() { releaseServer(t, script, strings.Repeat("0", 64), trusted) },
	} {
		setup()
		dir := filepath.Join(t.TempDir(), "claude-code")
		_, err := InstallClaudeCodeIn(context.Background(), dir, func(string) {})
		if err == nil || !strings.Contains(err.Error(), "verification") {
			t.Fatalf("%s: %v", name, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("%s: %d files left", name, len(entries))
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s: an unverified download ran", name)
		}
	}

	releaseServer(t, []byte("#!/bin/sh\nexit 3\n"), "", trusted)
	dir := filepath.Join(t.TempDir(), "claude-code")
	if _, err := InstallClaudeCodeIn(context.Background(), dir, func(string) {}); err == nil || !strings.Contains(err.Error(), "does not start") {
		t.Fatalf("broken binary: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("broken binary: %d files left", len(entries))
	}
}
