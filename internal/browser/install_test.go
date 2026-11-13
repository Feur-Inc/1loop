// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeChromium puts a "browser" script in 1loop's Chromium folder, as an
// install leaves it, and stops the daily update from going online.
func fakeChromium(t *testing.T, script string) (dir, path string) {
	t.Helper()
	if chromiumPlatform() == "" || debianTriplet() == "" {
		t.Skip("no Chrome for Testing build for this system")
	}
	t.Setenv("PATH", t.TempDir()) // no browser on the computer
	BrowserProfileOverride = filepath.Join(t.TempDir(), "browser")
	t.Cleanup(func() { BrowserProfileOverride = "" })
	dir = ChromiumDir()
	path = chromiumExecutable(dir, "150.0.1.2")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "checked"), nil, 0600)
	return dir, path
}

// Install Chromium overrides ONELOOP_BROWSER=none (the slim image): once
// 1loop's Chromium is in the data folder, Browser Use starts it. Without
// it, the server still has none. (Only what existed before, so this test
// fails on the code without Install Chromium.)
func TestInstalledChromiumOverridesNone(t *testing.T) {
	if chromiumPlatform() == "" {
		t.Skip("no Chrome for Testing build for this system")
	}
	t.Setenv("ONELOOP_BROWSER", "none")
	t.Setenv("ONELOOP_CHROMIUM", "/usr/bin/chromium")
	BrowserProfileOverride = filepath.Join(t.TempDir(), "browser")
	t.Cleanup(func() { BrowserProfileOverride = "" })
	if !Disabled() {
		t.Fatal("no Chromium installed: Browser Use stays off")
	}
	dir := ChromiumDir()
	path := chromiumExecutable(dir, "150.0.1.2")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("#!/bin/sh\n"), 0755)
	os.WriteFile(filepath.Join(dir, "checked"), nil, 0600) // no update check online
	if Disabled() {
		t.Fatal("an installed Chromium must turn Browser Use on despite ONELOOP_BROWSER=none")
	}
	binary, err := findBrowser(context.Background())
	if err != nil || binary.path != path || !binary.own {
		t.Fatalf("findBrowser: %+v %v, want %s", binary, err, path)
	}
}

// The libraries Install Chromium unpacked beside it go to the browser's
// loader, and its fonts to fontconfig.
func TestOwnChromiumEnv(t *testing.T) {
	t.Setenv("ONELOOP_BROWSER", "none")
	dir, path := fakeChromium(t, "")
	binary, err := findBrowser(context.Background())
	if err != nil || binary.path != path || binary.env != nil || !Installed() {
		t.Fatalf("no libraries: %+v %v", binary, err)
	}
	os.MkdirAll(chromiumLibraries(dir), 0755)
	os.WriteFile(filepath.Join(dir, "fonts.conf"), nil, 0644)
	binary, _ = findBrowser(context.Background())
	if len(binary.env) != 2 || !strings.HasPrefix(binary.env[0], "LD_LIBRARY_PATH="+filepath.Join(chromiumLibraries(dir), "usr", "lib", debianTriplet())) || binary.env[1] != "FONTCONFIG_FILE="+filepath.Join(dir, "fonts.conf") {
		t.Fatalf("env: %q", binary.env)
	}
}

// An installed Chromium that starts is kept; one that cannot start, here
// for a missing library with no apt to get it, is removed again, so the
// server does not report a browser it cannot run.
func TestInstallChromiumChecksItStarts(t *testing.T) {
	t.Setenv("ONELOOP_BROWSER", "none")
	_, path := fakeChromium(t, "echo Chromium 150\n")
	var said []string
	got, err := InstallChromium(context.Background(), func(text string) { said = append(said, text) })
	if err != nil || got != path || len(said) == 0 {
		t.Fatalf("install: %q %v %q", got, err, said)
	}

	_, path = fakeChromium(t, "echo 'chrome: error while loading shared libraries: libnss3.so: cannot open shared object file' >&2\nexit 127\n")
	_, err = InstallChromium(context.Background(), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "libnss3.so") || !strings.Contains(err.Error(), "no apt") {
		t.Fatalf("missing library: %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil || Installed() || !Disabled() {
		t.Fatal("a Chromium that cannot start must be removed")
	}
}

func TestChromiumPackages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deb.deps")
	os.WriteFile(path, []byte("ca-certificates\nfonts-liberation\nlibasound2 (>= 1.0.17)\nlibc6 (>= 2.25)\nlibcurl3-gnutls | libcurl3-nss | libcurl4 | libcurl3\nlibgtk-3-0 (>= 3.9.10) | libgtk-4-1\nlibnss3 (>= 2:3.35)\nevil;rm -rf /\nwget\n"), 0644)
	got, err := chromiumPackages(path)
	if want := []string{"fonts-liberation", "libasound2", "libnss3"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("packages: %q %v, want %q", got, err, want)
	}
}
