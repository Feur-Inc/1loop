// SPDX-License-Identifier: MPL-2.0

//go:build linux && cgo

package ui

// When GLFW finds no OpenGL configuration (a driver that fails, a machine
// without a GPU), 1loop starts again with the system's Mesa drawing on the
// CPU (LIBGL_ALWAYS_SOFTWARE), then, from Wayland, with its X11 build. The
// packages depend on Mesa (packaging/linux/build.sh), so it is there.

import (
	"os"
	"os/exec"
	"strings"
	"syscall"

	"fyne.io/fyne/v2"
)

// softwareVariable says 1loop asked for LIBGL_ALWAYS_SOFTWARE itself, so it
// takes it back from what it starts once the window is shown.
const softwareVariable = "ONELOOP_SOFTWARE_GL"

func softwareRendererStart() {}

// windowMissing: on Linux GLFW panics instead (showFirstWindow).
func windowMissing(fyne.Window) bool { return false }

func rendererReady() {
	forgetTried()
	if os.Getenv(softwareVariable) == "1" {
		rendererLogf("drawing with Mesa on the CPU (llvmpipe)")
		os.Unsetenv(softwareVariable)
		os.Unsetenv("LIBGL_ALWAYS_SOFTWARE")
	}
}

// renderFailed tries the next way of drawing in place of this process, and
// when none is left says so. It never returns.
func renderFailed(failure error, release func()) {
	release()
	software := os.Getenv("LIBGL_ALWAYS_SOFTWARE") != ""
	exe, _ := os.Executable()
	switch {
	case !software && exe != "":
		noteTried("The graphics driver ("+backendLabel()+")", failure)
		os.Setenv("LIBGL_ALWAYS_SOFTWARE", "1")
		os.Setenv(softwareVariable, "1")
		rendererLogf("starting again with Mesa on the CPU")
		syscall.Exec(exe, append([]string{exe}, os.Args[1:]...), os.Environ())
	case builtBackend == backendWayland && os.Getenv("DISPLAY") != "" && exe != "":
		noteTried("Mesa on the CPU, under Wayland", failure)
		if target := siblingBinary(exe, backendX11); target != "" {
			if _, err := os.Stat(target); err == nil {
				os.Setenv("ONELOOP_X11_FALLBACK", "1")
				rendererLogf("starting again with X11")
				syscall.Exec(target, append([]string{target}, os.Args[1:]...), os.Environ())
			}
		}
	default:
		noteTried("Mesa on the CPU ("+backendLabel()+")", failure)
	}
	text := renderFailureText(triedSoFar(), []string{
		"Check that Mesa is installed: sudo apt install libgl1-mesa-dri libglx-mesa0 libegl-mesa0 (Debian, Ubuntu), sudo dnf install mesa-dri-drivers (Fedora).",
		"Install or update the graphics driver, then start 1loop again.",
		"In a virtual machine, turn on 3D acceleration, or start 1loop from a desktop session rather than over SSH.",
	})
	os.Stderr.WriteString(renderFailureTitle + "\n\n" + text + "\n")
	showLinuxMessage(renderFailureTitle, text)
	os.Exit(1)
}

// showLinuxMessage shows the message with a tool that needs no OpenGL,
// the first one installed; without any, it was on stderr.
func showLinuxMessage(title, text string) {
	tools := [][]string{
		{"zenity", "--error", "--no-markup", "--width=560", "--title=" + title, "--text=" + text},
		{"kdialog", "--title", title, "--error", text},
		{"xmessage", "-center", title + "\n\n" + text},
		{"notify-send", "-u", "critical", title, strings.SplitN(text, "\n", 2)[0]},
	}
	for _, tool := range tools {
		if path, err := exec.LookPath(tool[0]); err == nil {
			if exec.Command(path, tool[1:]...).Run() == nil {
				return
			}
		}
	}
}
