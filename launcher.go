//go:build !server

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// appID is the window's application id (Wayland) and class (X11). The
// desktop finds the application's name and icon in the launcher entry that
// carries the same name.
const appID = "1loop"

// installLauncher registers 1loop with the desktop, in the user's own data
// folder: without an entry matching the window, GNOME shows "Unknown" and a
// generic icon in the dock, the overview and its "not responding" dialog.
// The entry points at this binary, so it is rewritten only when the binary
// has moved; it also puts 1loop in the applications menu.
func installLauncher() {
	if runtime.GOOS != "linux" {
		return
	}
	exe, err := os.Executable()
	if err != nil || strings.HasPrefix(exe, os.TempDir()) { // go run, tests: nothing to point at
		return
	}
	// The plain binary picks the right backend (X11 or Wayland) itself.
	if plain := strings.TrimSuffix(exe, "-wayland"); plain != exe {
		if _, err := os.Stat(plain); err == nil {
			exe = plain
		}
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		data = filepath.Join(home, ".local", "share")
	}
	icon := filepath.Join(data, "icons", "hicolor", "512x512", "apps", appID+".png")
	entry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=1loop
Comment=Your project. Your model. One loop.
Exec="%s"
Icon=%s
Terminal=false
Categories=Development;
StartupWMClass=%s
StartupNotify=true
`, strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(exe), icon, appID)
	if art := launcherIcon(); art != nil {
		writeIfChanged(icon, art)
	}
	writeIfChanged(filepath.Join(data, "applications", appID+".desktop"), []byte(entry))
}

// launcherIcon is the ring on a black rounded square, with the margin the
// desktop's icon grid expects (the bare artwork fills its square edge to
// edge and looked like a tile among the other applications).
func launcherIcon() []byte {
	const size, margin, radius = 512, 34, 104
	icon := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// Distance past the rounded square's edge, for a smooth rim.
			dx := math.Max(math.Max(float64(margin+radius-x), float64(x-(size-1-margin-radius))), 0)
			dy := math.Max(math.Max(float64(margin+radius-y), float64(y-(size-1-margin-radius))), 0)
			inside := x >= margin && x < size-margin && y >= margin && y < size-margin
			if cover := math.Min(1, math.Max(0, radius+0.5-math.Hypot(dx, dy))); inside && cover > 0 {
				icon.SetNRGBA(x, y, color.NRGBA{A: uint8(cover * 255)})
			}
		}
	}
	const ring = 330
	at := image.Pt((size-ring)/2, (size-ring)/2)
	data, _ := artwork.ReadFile("assets/1loop-anneau.png") // decoded here: this runs beside the interface
	art, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	draw.Draw(icon, image.Rect(at.X, at.Y, at.X+ring, at.Y+ring), scaleTo(art, ring, ring), image.Point{}, draw.Over)
	var out bytes.Buffer
	png.Encode(&out, icon)
	return out.Bytes()
}

func writeIfChanged(path string, content []byte) {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(content) {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0755) == nil {
		os.WriteFile(path, content, 0644)
	}
}
