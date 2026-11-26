// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"1loop/internal/core"
)

// One official Debian bookworm-slim arm64 layer, pinned independently of the
// mutable image tag. apt uses Debian's signed repositories after installation.
const mobileDebianLayer = "c75f989a229d12b2d2613a5997de9ff3546f664c22da9248720033a2410220f6"
const mobileDebianBytes = 28137179

// The pinned public release key is the one documented by Anthropic with
// fingerprint 31DDDE24DDFAB679F42D7BD2BAA929FF1A7ECACE. apt verifies releases.
func installMobileClaude(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, "GET", "https://downloads.claude.ai/keys/claude-code.asc", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	key, err := io.ReadAll(io.LimitReader(response.Body, 8192))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(key)
	if response.StatusCode != http.StatusOK || hex.EncodeToString(digest[:]) != "bd70a5e4a268002704024ceba7f8446024114e94f3f0bdd11c23a9e592be81c6" {
		return fmt.Errorf("The Claude Code release key failed verification")
	}
	if err := os.MkdirAll("/etc/apt/keyrings", 0755); err != nil {
		return err
	}
	if err := os.WriteFile("/etc/apt/keyrings/1loop-claude.asc", key, 0644); err != nil {
		return err
	}
	if err := os.WriteFile("/etc/apt/sources.list.d/1loop-claude.list", []byte("deb [signed-by=/etc/apt/keyrings/1loop-claude.asc] https://downloads.claude.ai/claude-code/apt/stable stable main\n"), 0644); err != nil {
		return err
	}
	for _, arguments := range [][]string{{"dpkg", "--configure", "-a"}, {"apt-get", "update"}, {"apt-get", "install", "-y", "--no-install-recommends", "ca-certificates", "claude-code"}} {
		command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			return err
		}
	}
	return nil
}

func installMobileDebian(ctx context.Context, destination string) (err error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/debian:pull", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	var authorization struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&authorization)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || authorization.Token == "" {
		return fmt.Errorf("Unable to authorize the Debian download (HTTP %d)", response.StatusCode)
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, "https://registry-1.docker.io/v2/library/debian/blobs/sha256:"+mobileDebianLayer, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+authorization.Token)
	response, err = client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Unable to download Debian (HTTP %d)", response.StatusCode)
	}
	// The phone records destination before launch, so a killed installer's
	// archive can also be recovered by its exact name on the next attempt.
	archive, err := os.OpenFile(destination+".tar.gz", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, archive.Close(), os.Remove(archive.Name())) }()
	fmt.Println("Downloading Debian (28 MB)…")
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(archive, digest), io.LimitReader(response.Body, mobileDebianBytes+1))
	if err != nil {
		return err
	}
	if written != mobileDebianBytes || hex.EncodeToString(digest.Sum(nil)) != mobileDebianLayer {
		return fmt.Errorf("The Debian download failed its checksum or size check")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	fmt.Println("Installing Debian…")
	if err := extractMobileDebian(destination, archive); err != nil {
		return err
	}
	fmt.Println("Debian installed.")
	return nil
}

// extractMobileDebian unpacks the Debian root (a gzipped tar) into
// destination, which it creates. It is a root filesystem: its links may
// name absolute paths, read from that root once installed. No archive link
// exists while files are written, and links are made only in folders that
// really are inside the root, so nothing reaches the phone's Android
// filesystem (core.Unpack, #524).
func extractMobileDebian(destination string, archive io.Reader) (err error) {
	root, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := os.Mkdir(root, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(root))
		}
	}()
	_, err = core.Unpack{
		Check: func(name string) error {
			if strings.HasPrefix(path.Base(name), ".wh.") {
				return fmt.Errorf("Unsupported Debian archive path %q", name)
			}
			return nil
		},
		Mode:          func(perm os.FileMode) os.FileMode { return perm },
		Links:         true,
		AbsoluteLinks: true,
		// Android's app SELinux policy can prohibit hardlinks even when both
		// files belong to the app: they become copies, the same contents.
		HardLinks: true,
		MaxTotal:  1 << 30,
	}.Tar(archive, root)
	return err
}
