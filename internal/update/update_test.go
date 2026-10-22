package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
}

func sign(t *testing.T, key *ecdsa.PrivateKey, manifest Manifest) []byte {
	payload, _ := json.Marshal(manifest)
	digest := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(signed{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(signature)})
	return data
}

func TestCode(t *testing.T) {
	for version, code := range map[string]int{"0.2.0": 200, "v1.2.3": 10203, "10.0.1": 100001, "dev": 0, "1.2": 0, "1.100.0": 0} {
		if Code(version) != code {
			t.Errorf("Code(%q) = %d, want %d", version, Code(version), code)
		}
	}
}

func TestVerify(t *testing.T) {
	key, public := testKey(t)
	manifest := Manifest{Version: "0.2.1", Code: 201, Files: map[string]File{"android": {URL: "https://x/a.apk", SHA256: "00", Size: 1}}}
	got, err := Verify(sign(t, key, manifest), public)
	if err != nil || got.Version != "0.2.1" || got.Files["android"].URL != "https://x/a.apk" {
		t.Fatalf("a signed manifest verifies: %v %+v", err, got)
	}
	other, _ := testKey(t)
	if _, err := Verify(sign(t, other, manifest), public); err == nil {
		t.Fatal("a manifest signed by another key is refused")
	}
	data := sign(t, key, manifest)
	var envelope signed
	json.Unmarshal(data, &envelope)
	payload, _ := base64.StdEncoding.DecodeString(envelope.Payload)
	tampered := append([]byte{}, payload...)
	tampered[len(tampered)-2] ^= 1
	envelope.Payload = base64.StdEncoding.EncodeToString(tampered)
	data, _ = json.Marshal(envelope)
	if _, err := Verify(data, public); err == nil {
		t.Fatal("a changed manifest is refused")
	}
	if _, err := Verify(sign(t, key, Manifest{Version: "0.2.1", Code: 999}), public); err == nil {
		t.Fatal("a code that does not match the version is refused")
	}
}

// The embedded key must be a real key, not the placeholder.
func TestEmbeddedKey(t *testing.T) {
	block, _ := pem.Decode([]byte(publicKey))
	if block == nil {
		t.Fatal("no release key in this build")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadChecksTheFile(t *testing.T) {
	content := []byte("the new 1loop")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
	defer server.Close()
	sum := sha256.Sum256(content)
	destination := filepath.Join(t.TempDir(), "file")
	if err := Download(context.Background(), File{URL: server.URL, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content))}, destination); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), File{URL: server.URL, SHA256: "00", Size: int64(len(content))}, destination); err == nil {
		t.Fatal("a file that does not match the release is refused")
	}
	if _, err := os.Stat(destination); err == nil {
		t.Fatal("a refused download is not kept")
	}
}

func TestUnpackAndReplace(t *testing.T) {
	dir, staging := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(dir, "1loop"), []byte("old"), 0755)
	// A tar.gz with a file, a nested one and an entry trying to leave.
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	out, _ := os.Create(archive)
	zipped := gzip.NewWriter(out)
	writer := tar.NewWriter(zipped)
	for name, text := range map[string]string{"1loop": "new", "software/x": "nested"} {
		writer.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(text)), Typeflag: tar.TypeReg})
		writer.Write([]byte(text))
	}
	writer.Close()
	zipped.Close()
	out.Close()
	if err := extractTar(archive, staging); err != nil {
		t.Fatal(err)
	}
	if err := replace(staging, dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "1loop")); string(data) != "new" {
		t.Fatal("the binary is replaced")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "1loop.old")); string(data) != "old" {
		t.Fatal("the previous binary is kept as .old")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "software", "x")); string(data) != "nested" {
		t.Fatal("nested files are installed")
	}
	// A zip entry that tries to leave the folder is refused.
	evil := filepath.Join(t.TempDir(), "evil.zip")
	file, _ := os.Create(evil)
	zw := zip.NewWriter(file)
	w, _ := zw.Create("../outside")
	w.Write([]byte("x"))
	zw.Close()
	file.Close()
	if err := extractZip(evil, t.TempDir()); err == nil {
		t.Fatal("an entry outside the folder is refused")
	}
}
