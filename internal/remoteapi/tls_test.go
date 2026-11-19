// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCertificateGeneratedOnceAndPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "remote")
	first, err := Certificate(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"server.key", "server.crt"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		// Windows has no mode bits: the user profile's ACL protects the folder.
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("%s has mode %v", name, info.Mode().Perm())
		}
	}
	second, err := Certificate(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(first) != Fingerprint(second) || len(Fingerprint(first)) != 64 {
		t.Fatalf("fingerprint changed between starts: %s %s", Fingerprint(first), Fingerprint(second))
	}
	if _, err := Certificate(dir, filepath.Join(dir, "server.crt"), ""); err == nil {
		t.Fatal("a certificate without its key was accepted")
	}
}

// One of the two files gone: a clear message, and nothing regenerated (a new
// certificate would unpair every device).
func TestCertificateWithAMissingFile(t *testing.T) {
	for _, missing := range []string{"server.key", "server.crt"} {
		dir := filepath.Join(t.TempDir(), "remote")
		if _, err := Certificate(dir, "", ""); err != nil {
			t.Fatal(err)
		}
		os.Remove(filepath.Join(dir, missing))
		_, err := Certificate(dir, "", "")
		if err == nil || !strings.Contains(err.Error(), missing+" is missing") {
			t.Fatalf("%s removed: %v", missing, err)
		}
		if _, err := os.Stat(filepath.Join(dir, missing)); !os.IsNotExist(err) {
			t.Fatalf("%s was regenerated", missing)
		}
	}
}
