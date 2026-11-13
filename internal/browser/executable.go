// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// NoBrowserProblem is what Browser Use answers where ONELOOP_BROWSER=none
// turns it off: the slim server image, which has no Chromium.
const NoBrowserProblem = "This server has no browser: it runs without Chromium (ONELOOP_BROWSER=none, as in the slim Docker image). Install Chromium from Settings, or use the server image with Chromium for Browser Use."

// Disabled says whether Browser Use is off here: ONELOOP_BROWSER=none, and
// no Chromium installed since with Install Chromium (InstallChromium). No
// browser is looked for then, and none is downloaded.
func Disabled() bool {
	return noneAsked() && !Installed()
}

// noneAsked is ONELOOP_BROWSER=none, the slim image's.
func noneAsked() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ONELOOP_BROWSER")), "none")
}

// browserBinary is the browser to start, with what its process needs.
type browserBinary struct {
	path string
	env  []string
	own  bool // 1loop's Chromium (chromium.go)
}

// findBrowser is the browser Browser Use starts: ONELOOP_CHROMIUM (the Nix
// package sets it), else a Chromium, Chrome or Edge on the computer, else
// 1loop's own Chromium, downloaded the first time. With ONELOOP_BROWSER=none,
// only 1loop's Chromium once Install Chromium put it there, else none.
func findBrowser(ctx context.Context) (browserBinary, error) {
	own := func() (browserBinary, error) {
		path, err := ownChromium(ctx)
		return browserBinary{path: path, env: ownChromiumEnv(ChromiumDir()), own: true}, err
	}
	if noneAsked() {
		if Installed() {
			return own()
		}
		return browserBinary{}, errors.New(NoBrowserProblem)
	}
	if path := os.Getenv("ONELOOP_CHROMIUM"); path != "" {
		return browserBinary{path: path}, nil
	}
	if os.Getenv("ONELOOP_FLATPAK") != "" {
		// The browser runs on the host, through the host-browser launcher
		// linked as chromium: the host's own browser, else 1loop's
		// Chromium, whose folder the host shares.
		launcher, err := exec.LookPath("chromium")
		if err != nil {
			return browserBinary{}, err
		}
		if exec.Command("/app/lib/1loop/host/host-run", "sh", "-c", "command -v chromium || command -v chromium-browser || command -v google-chrome-stable || command -v google-chrome").Run() == nil {
			return browserBinary{path: launcher}, nil
		}
		path, err := ownChromium(ctx)
		return browserBinary{path: launcher, env: []string{"ONELOOP_OWN_CHROMIUM=" + path}, own: true}, err
	}
	if path := browserExecutable(); path != "" {
		return browserBinary{path: path}, nil
	}
	return own()
}

func browserExecutable() string {
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome-stable", "google-chrome", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("PROGRAMFILES"), os.Getenv("PROGRAMFILES(X86)"), os.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			for _, relative := range []string{"Google/Chrome/Application/chrome.exe", "Microsoft/Edge/Application/msedge.exe", "Chromium/Application/chrome.exe"} {
				path := filepath.Join(root, filepath.FromSlash(relative))
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					return path
				}
			}
		}
	}
	return ""
}
