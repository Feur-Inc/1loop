// Package update keeps 1loop up to date from its release page: a signed
// manifest lists the latest version and a file for each platform. The
// desktop and the server replace their own binaries; the phone app reads
// the same manifest (Updates.kt).
package update

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Version is this build's version, set at build time from the VERSION file
// (-ldflags "-X 1loop/internal/update.Version=…"). "dev" builds do not
// update themselves.
var Version = "dev"

// ManifestURL is the release page's manifest. ONELOOP_UPDATE_URL replaces
// it, to test a release before publishing it.
const ManifestURL = "https://tpm28.github.io/1loop/updates/latest.json"

// publicKey verifies the manifest's signature: only the holder of the
// matching private key (kept off the repository) can publish an update.
const publicKey = `-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAET3kQmwcR+o8F/efaAYkwc78WcTV1
tO/sbBPxLH32uNqtzGn3WqfliMODCiLFoCT2dL0IrHJdBAiXQ+BXn5eSTw==
-----END PUBLIC KEY-----`

// File is a platform's download.
type File struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is the latest release.
type Manifest struct {
	Version string          `json:"version"`
	Code    int             `json:"code"`
	Date    string          `json:"date"`
	Notes   string          `json:"notes"`
	Page    string          `json:"page"` // where to download by hand
	Files   map[string]File `json:"files"`
}

// signed is the manifest as published: the exact bytes that were signed,
// and their signature (ECDSA P-256, SHA-256, ASN.1).
type signed struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// Code orders versions: "1.2.3" is 10203. Anything else is 0.
func Code(version string) int {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) != 3 {
		return 0
	}
	code := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 99 {
			return 0
		}
		code = code*100 + n
	}
	return code
}

// Platform names this binary's file in the manifest: "desktop" or
// "server", then the system and architecture.
func Platform(kind string) string {
	return kind + "-" + runtime.GOOS + "-" + runtime.GOARCH
}

func manifestURL() string {
	if url := os.Getenv("ONELOOP_UPDATE_URL"); url != "" {
		return url
	}
	return ManifestURL
}

var client = &http.Client{Timeout: 30 * time.Second}

// Fetch reads and verifies the latest manifest.
func Fetch(ctx context.Context) (Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", manifestURL(), nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("Cannot reach the release page: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("The release page answered %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Manifest{}, err
	}
	return Verify(data, publicKey)
}

// Verify checks a published manifest against a public key and decodes it.
func Verify(data []byte, keyPEM string) (Manifest, error) {
	var envelope signed
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Manifest{}, fmt.Errorf("Invalid update manifest")
	}
	payload, err := base64.StdEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return Manifest{}, fmt.Errorf("Invalid update manifest")
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		return Manifest{}, fmt.Errorf("Invalid update signature")
	}
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return Manifest{}, fmt.Errorf("No update key in this build")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	key, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok {
		return Manifest{}, fmt.Errorf("Invalid update key in this build")
	}
	digest := sha256.Sum256(payload)
	if !ecdsa.VerifyASN1(key, digest[:], signature) {
		return Manifest{}, fmt.Errorf("The update manifest is not signed by 1loop's release key")
	}
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("Invalid update manifest")
	}
	if manifest.Code != Code(manifest.Version) || manifest.Code == 0 {
		return Manifest{}, fmt.Errorf("Invalid update version")
	}
	return manifest, nil
}

// Status is what a client shows about updates.
type Status struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Page      string `json:"page,omitempty"`
	Available bool   `json:"available"`
	CanUpdate bool   `json:"canUpdate"`         // this binary can replace itself
	Problem   string `json:"problem,omitempty"` // why it cannot, or why the check failed
}

// Check compares the latest release with this build, for a platform kind.
func Check(ctx context.Context, kind string) (Status, Manifest) {
	status := Status{Current: Version}
	manifest, err := Fetch(ctx)
	if err != nil {
		status.Problem = err.Error()
		return status, manifest
	}
	status.Latest, status.Notes, status.Page = manifest.Version, manifest.Notes, manifest.Page
	status.Available = manifest.Code > Code(Version)
	if _, ok := manifest.Files[Platform(kind)]; !ok {
		status.Problem = "No download for " + Platform(kind) + " in this release: download it from the release page."
		return status, manifest
	}
	if Code(Version) == 0 {
		status.Problem = "This is a development build: it does not update itself."
		return status, manifest
	}
	status.CanUpdate = status.Available
	return status, manifest
}

// Download saves a release file, checking its size and SHA-256.
func Download(ctx context.Context, file File, destination string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", file.URL, nil)
	if err != nil {
		return err
	}
	downloads := &http.Client{} // no overall timeout: a large file on a slow line
	response, err := downloads.Do(req)
	if err != nil {
		return fmt.Errorf("Download failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Download failed: %s", response.Status)
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), io.LimitReader(response.Body, file.Size+1))
	closeErr := out.Close()
	switch {
	case copyErr != nil:
		err = copyErr
	case closeErr != nil:
		err = closeErr
	case written != file.Size:
		err = fmt.Errorf("The download is incomplete")
	case hex.EncodeToString(hasher.Sum(nil)) != strings.ToLower(file.SHA256):
		err = fmt.Errorf("The download does not match the release: it was not installed")
	}
	if err != nil {
		os.Remove(destination)
	}
	return err
}
