// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// A certificate past its dates, as every server's is after its first year,
// is loaded as it is: never renewed or replaced, so the fingerprint paired
// devices pin stays the same (#517).
func TestExpiredCertificateIsKept(t *testing.T) {
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().AddDate(-2, 0, 0), NotAfter: time.Now().AddDate(-1, 0, 0)}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, _ := x509.MarshalPKCS8PrivateKey(key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	os.WriteFile(filepath.Join(dir, "server.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600)
	os.WriteFile(filepath.Join(dir, "server.crt"), certPEM, 0600)
	certificate, err := Certificate(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if Fingerprint(certificate) != hex.EncodeToString(sum[:]) {
		t.Fatal("the expired certificate was replaced")
	}
	if kept, _ := os.ReadFile(filepath.Join(dir, "server.crt")); !bytes.Equal(kept, certPEM) {
		t.Fatal("server.crt was rewritten")
	}
}

// Both files there but unusable (damaged, or the halves of two
// certificates): both named, with what to do, and neither replaced.
func TestCertificateWithADamagedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "remote")
	if _, err := Certificate(dir, "", ""); err != nil {
		t.Fatal(err)
	}
	certPEM, _ := os.ReadFile(filepath.Join(dir, "server.crt"))
	os.WriteFile(filepath.Join(dir, "server.key"), []byte("not a key"), 0600)
	_, err := Certificate(dir, "", "")
	if err == nil || !strings.Contains(err.Error(), "server.key do not make a certificate") || !strings.Contains(err.Error(), "delete both") {
		t.Fatalf("damaged key: %v", err)
	}
	if kept, _ := os.ReadFile(filepath.Join(dir, "server.crt")); !bytes.Equal(kept, certPEM) {
		t.Fatal("server.crt was replaced")
	}
	if key, _ := os.ReadFile(filepath.Join(dir, "server.key")); string(key) != "not a key" {
		t.Fatal("server.key was replaced")
	}
	// The files given on the command line are named too.
	_, err = Certificate(dir, filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"))
	if err == nil || !strings.Contains(err.Error(), "-tls-cert") || !strings.Contains(err.Error(), "missing.crt") {
		t.Fatalf("missing -tls-cert: %v", err)
	}
}
