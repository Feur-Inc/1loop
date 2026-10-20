package remoteapi

import (
	"os"
	"path/filepath"
	"runtime"
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
