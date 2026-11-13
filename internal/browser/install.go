// SPDX-License-Identifier: MPL-2.0

package browser

// Install Chromium (issue #406): the server's button for an image or a
// computer without a browser. It downloads 1loop's Chromium (chromium.go)
// into the data folder, checked against Google's checksum, and then makes
// sure it starts. Where it lacks system libraries, as in the slim Docker
// image, they come from the system's own Debian or Ubuntu packages, fetched
// without administrator rights by apt, which checks their signatures, and
// unpacked beside the browser. Once installed, it overrides
// ONELOOP_BROWSER=none.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// Installed says whether 1loop's Chromium is in its folder: downloaded by
// Install Chromium, or by Browser Use the first time on a computer without
// a browser.
func Installed() bool {
	dir := ChromiumDir()
	return dir != "" && chromiumPlatform() != "" && len(installedChromium(dir)) > 0
}

// InstallChromium downloads 1loop's Chromium unless it is there, with the
// system libraries it lacks, and checks that it starts. A Chromium that
// cannot start is removed, so the server never says it has a browser it
// cannot run.
func InstallChromium(ctx context.Context, progress func(string)) (string, error) {
	stop := watchChromium(func(text string) {
		if text != "" {
			progress(text)
		}
	})
	path, err := ownChromium(ctx)
	stop()
	if err != nil {
		return "", err
	}
	dir := ChromiumDir()
	version := filepath.Dir(filepath.Dir(path)) // dir/<version>/chrome-<platform>/chrome
	progress("Checking that Chromium starts…")
	failed := chromiumStarts(ctx, path, ownChromiumEnv(dir))
	if failed != nil && runtime.GOOS == "linux" && missingLibrary.MatchString(failed.Error()) {
		if err := installChromiumLibraries(ctx, dir, filepath.Dir(path), progress); err != nil {
			os.RemoveAll(version)
			return "", fmt.Errorf("%v. %w", chromiumStartError(failed), err)
		}
		progress("Checking that Chromium starts…")
		failed = chromiumStarts(ctx, path, ownChromiumEnv(dir))
	}
	if failed != nil {
		os.RemoveAll(version)
		return "", fmt.Errorf("Chromium was downloaded but does not start: %w", chromiumStartError(failed))
	}
	return path, nil
}

// chromiumStarts runs the browser for its version: the loader fails at
// once on a missing library.
func chromiumStarts(ctx context.Context, path string, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	command.Env = append(os.Environ(), env...)
	output, err := command.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if len(text) > 400 {
			text = text[:400] + "…"
		}
		return fmt.Errorf("%v: %s", err, text)
	}
	return nil
}

// chromiumLibraries is where the system libraries 1loop's Chromium needs
// are unpacked, as a file system root (usr/lib/…).
func chromiumLibraries(dir string) string {
	return filepath.Join(dir, "libraries")
}

// debianTriplet is Debian's folder for this system's libraries.
func debianTriplet() string {
	return map[string]string{"amd64": "x86_64-linux-gnu", "arm64": "aarch64-linux-gnu"}[runtime.GOARCH]
}

// ownChromiumEnv is what 1loop's Chromium needs to start with the libraries
// unpacked beside it, or nothing when there are none.
func ownChromiumEnv(dir string) []string {
	if dir == "" {
		return nil
	}
	root := chromiumLibraries(dir)
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	paths := []string{
		filepath.Join(root, "usr", "lib", debianTriplet()),
		filepath.Join(root, "lib", debianTriplet()),
		filepath.Join(root, "usr", "lib"),
	}
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		paths = append(paths, existing)
	}
	env := []string{"LD_LIBRARY_PATH=" + strings.Join(paths, string(os.PathListSeparator))}
	if fonts := filepath.Join(dir, "fonts.conf"); fileExists(fonts) {
		env = append(env, "FONTCONFIG_FILE="+fonts)
	}
	return env
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// The packages of deb.deps that Chromium does not need to start headless,
// or that a system always has: the C library, the desktop's GTK, the tools
// its .deb package would use. Vulkan comes with Chromium.
var skippedPackages = []string{"libc6", "ca-certificates", "wget", "xdg-utils", "libgtk-3-0", "libvulkan1", "libudev1", "libcurl3-gnutls"}

// chromiumPackages reads the Debian packages Chromium needs from the
// deb.deps file Chrome for Testing ships, first choice of each.
func chromiumPackages(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var packages []string
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		name, _, _ := strings.Cut(lines.Text(), "|")
		name, _, _ = strings.Cut(strings.TrimSpace(name), " ")
		if name == "" || slices.Contains(skippedPackages, name) || strings.ContainsAny(name, "/;&$`'\"\\") {
			continue
		}
		packages = append(packages, name)
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("%s lists no package", filepath.Base(path))
	}
	return packages, lines.Err()
}

// installChromiumLibraries fetches the Debian packages Chromium needs with
// apt, without administrator rights: apt keeps its lists and downloads in
// a folder of dir and checks the repositories' signatures and the packages'
// checksums as for an install. Their files are unpacked, not installed, in
// chromiumLibraries(dir), and a fonts.conf points fontconfig to their fonts.
func installChromiumLibraries(ctx context.Context, dir, chrome string, progress func(string)) error {
	apt, err := exec.LookPath("apt-get")
	if err != nil {
		return errors.New("This system has no apt to download them: install them with its package manager")
	}
	dpkg, err := exec.LookPath("dpkg-deb")
	if err != nil {
		return errors.New("This system has no dpkg-deb to unpack them: install them with its package manager")
	}
	packages, err := chromiumPackages(filepath.Join(chrome, "deb.deps"))
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(dir, ".libraries-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	lists, archives, root := filepath.Join(work, "lists"), filepath.Join(work, "archives"), filepath.Join(work, "root")
	for _, folder := range []string{filepath.Join(lists, "partial"), filepath.Join(archives, "partial"), root} {
		if err := os.MkdirAll(folder, 0755); err != nil {
			return err
		}
	}
	options := []string{"-q",
		"-o", "Dir::State::Lists=" + lists,
		"-o", "Dir::Cache=" + work,
		"-o", "Dir::Cache::archives=" + archives,
		"-o", "Debug::NoLocking=1", // its own folders: no system lock to take, and no root to take it
	}
	if me, err := user.Current(); err == nil {
		options = append(options, "-o", "APT::Sandbox::User="+me.Username)
	}
	run := func(name string, arguments ...string) error {
		command := exec.CommandContext(ctx, name, arguments...)
		command.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
		output, err := command.CombinedOutput()
		if err != nil {
			text := strings.TrimSpace(string(output))
			if len(text) > 600 {
				text = "…" + text[len(text)-600:]
			}
			return fmt.Errorf("%s failed (%v): %s", filepath.Base(name), err, text)
		}
		return nil
	}
	progress("Downloading the system libraries Chromium needs (Debian packages)…")
	if err := run(apt, append(options, "update")...); err != nil {
		return err
	}
	// No --allow-unauthenticated: apt refuses a package it cannot verify.
	if err := run(apt, append(options, append([]string{"install", "--download-only", "--no-install-recommends", "-y"}, packages...)...)...); err != nil {
		return err
	}
	debs, _ := filepath.Glob(filepath.Join(archives, "*.deb"))
	progress("Unpacking the system libraries…")
	for _, deb := range debs {
		if err := run(dpkg, "-x", deb, root); err != nil {
			return err
		}
	}
	target := chromiumLibraries(dir)
	os.RemoveAll(target)
	if err := os.Rename(root, target); err != nil {
		return err
	}
	// fontconfig's own settings are in /etc/fonts, which a minimal system
	// may lack: the system's are kept when there, with the unpacked fonts.
	conf := `<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
<fontconfig>
  <include ignore_missing="yes">/etc/fonts/fonts.conf</include>
  <dir>` + xmlText(filepath.Join(target, "usr", "share", "fonts")) + `</dir>
  <cachedir>` + xmlText(filepath.Join(dir, "fontcache")) + `</cachedir>
` + fontAliases + `</fontconfig>
`
	return os.WriteFile(filepath.Join(dir, "fonts.conf"), []byte(conf), 0644)
}

// fontAliases give the generic families, and the fonts pages name most
// (Chromium's own defaults among them), the Liberation font of the same
// metrics: without /etc/fonts, fontconfig would take any font, monospace
// for a serif page.
var fontAliases = func() string {
	var aliases strings.Builder
	for _, fonts := range [][]string{
		{"Liberation Sans", "sans-serif", "Arial", "Helvetica", "Arimo"},
		{"Liberation Serif", "serif", "Times New Roman", "Times", "Tinos"},
		{"Liberation Mono", "monospace", "Courier New", "Courier", "Cousine"},
	} {
		for _, family := range fonts[1:] {
			aliases.WriteString(`  <alias binding="same"><family>` + family + `</family><prefer><family>` + fonts[0] + `</family></prefer></alias>` + "\n")
		}
	}
	return aliases.String()
}()

func xmlText(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}
