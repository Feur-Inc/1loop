package agent

// Claude Code on this computer: where its command is, and Anthropic's
// official installer, which 1loop runs when the user adds a Claude Code
// connection without it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const claudeDownloads = "https://downloads.claude.ai/claude-code-releases"

// ClaudeExecutable is the claude command: on PATH, else where Claude Code's
// installer puts it (~/.local/bin), a folder a desktop session's PATH often
// lacks.
func ClaudeExecutable() (string, error) {
	if path, err := exec.LookPath("claude"); err == nil {
		return path, nil
	}
	if home, err := os.UserHomeDir(); err == nil && os.Getenv("ONELOOP_FLATPAK") == "" {
		path := filepath.Join(home, ".local", "bin", "claude")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("Claude Code is not installed: no claude command was found")
}

// ClaudeInstallProblem says why 1loop cannot install Claude Code here, or "".
func ClaudeInstallProblem() string {
	if os.Getenv("ONELOOP_FLATPAK") != "" {
		return "The Flatpak cannot install Claude Code. Install it on the system with Anthropic's instructions (code.claude.com/docs)."
	}
	if _, err := os.Stat("/etc/NIXOS"); err == nil {
		return "On NixOS, add the claude-code package to your configuration."
	}
	if claudePlatform() == "" {
		return "Claude Code has no build for this system."
	}
	return ""
}

// claudePlatform is the name Claude Code's releases give this system, as
// its install scripts choose it.
func claudePlatform() string {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	switch {
	case arch == "":
		return ""
	case runtime.GOOS == "windows":
		if os.Getenv("PROCESSOR_ARCHITECTURE") == "ARM64" {
			arch = "arm64"
		}
		return "win32-" + arch
	case runtime.GOOS == "linux":
		for _, musl := range []string{"/lib/libc.musl-x86_64.so.1", "/lib/libc.musl-aarch64.so.1"} {
			if _, err := os.Stat(musl); err == nil {
				return "linux-" + arch + "-musl"
			}
		}
		return "linux-" + arch
	}
	return ""
}

var claudeRelease = regexp.MustCompile(`^\d+\.\d+\.\d+\S*$`)

// InstallClaudeCode does what Anthropic's install scripts do
// (claude.ai/install.sh and install.ps1): it downloads the latest release
// for this system, checks it against the SHA-256 of the release's manifest,
// then runs its `claude install`, which sets Claude Code up in ~/.local/bin
// for this user, without administrator rights.
func InstallClaudeCode(ctx context.Context, progress func(string)) (string, error) {
	if problem := ClaudeInstallProblem(); problem != "" {
		return "", fmt.Errorf("%s", problem)
	}
	target := claudePlatform()
	client := &http.Client{Timeout: 30 * time.Minute}
	get := func(address string, limit int64) ([]byte, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s answered %s", request.URL.Host, response.Status)
		}
		return io.ReadAll(io.LimitReader(response.Body, limit))
	}
	progress("Finding the latest Claude Code…")
	raw, err := get(claudeDownloads+"/latest", 256)
	if err != nil {
		return "", err
	}
	version := strings.TrimSpace(string(raw))
	if !claudeRelease.MatchString(version) {
		return "", fmt.Errorf("downloads.claude.ai did not give a version. It may be unreachable or unavailable in your region (anthropic.com/supported-countries).")
	}
	raw, err = get(claudeDownloads+"/"+version+"/manifest.json", 1<<20)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Platforms map[string]struct {
			Binary   string `json:"binary"`
			Checksum string `json:"checksum"`
			Size     int64  `json:"size"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", err
	}
	release, ok := manifest.Platforms[target]
	if !ok || len(release.Checksum) != 64 || release.Binary != filepath.Base(release.Binary) || release.Binary == "" {
		return "", fmt.Errorf("Claude Code %s has no build for %s", version, target)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	downloads := filepath.Join(home, ".claude", "downloads")
	if err := os.MkdirAll(downloads, 0755); err != nil {
		return "", err
	}
	name := "claude-" + version + "-" + target
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(downloads, name)
	defer os.Remove(binary) // the scripts' cleanup: `claude install` keeps its own copy
	if err := claudeDownload(ctx, client, claudeDownloads+"/"+version+"/"+target+"/"+release.Binary, binary, release.Checksum, release.Size, progress); err != nil {
		return "", err
	}
	progress("Setting up Claude Code…")
	setup, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(setup, binary, "install")
	command.Dir = home
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		text := strings.TrimSpace(output.String())
		if len(text) > 600 {
			text = "…" + text[len(text)-600:]
		}
		return "", fmt.Errorf("claude install failed (%v): %s", err, text)
	}
	return ClaudeExecutable()
}

// claudeDownload saves a release binary, refusing it unless its SHA-256 is
// the manifest's.
func claudeDownload(ctx context.Context, client *http.Client, address, path, checksum string, size int64, progress func(string)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", request.URL.Host, response.Status)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	sum := sha256.New()
	total, done, said := max(size, response.ContentLength), int64(0), -1
	buffer := make([]byte, 256<<10)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err := file.Write(buffer[:n]); err != nil {
				file.Close()
				return err
			}
			sum.Write(buffer[:n])
			done += int64(n)
			if percent := int(done * 100 / max(total, 1)); total > 0 && percent != said {
				said = percent
				progress(fmt.Sprintf("Downloading Claude Code… %d%% of %d MB", percent, total>>20))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return readErr
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != strings.ToLower(checksum) {
		return fmt.Errorf("the Claude Code download failed its checksum verification")
	}
	return nil
}
