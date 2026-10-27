package browser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// browserBinary is the browser to start, with what its process needs.
type browserBinary struct {
	path string
	env  []string
	own  bool // 1loop's Chromium (chromium.go)
}

// findBrowser is the browser Browser Use starts: ONELOOP_CHROMIUM (the Nix
// package sets it), else a Chromium, Chrome or Edge on the computer, else
// 1loop's own Chromium, downloaded the first time.
func findBrowser(ctx context.Context) (browserBinary, error) {
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
	path, err := ownChromium(ctx)
	return browserBinary{path: path, own: true}, err
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
