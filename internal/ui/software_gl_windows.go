// SPDX-License-Identifier: MPL-2.0

//go:build windows

package ui

// Without an OpenGL driver (a virtual machine, some remote desktops: only
// "Microsoft Basic Display Adapter"), GLFW cannot make the window. 1loop
// then draws with the Mesa it carries (internal/softgl), on the CPU: it
// extracts it in %LOCALAPPDATA%\1loop, remembers that this PC needs it, and
// starts again; later starts load it at once. In the background it checks
// whether the PC's driver works again, and uses it from the next start if
// so. Nothing is asked and nothing is said while one of them draws.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"fyne.io/fyne/v2"
	"github.com/go-gl/glfw/v3.3/glfw"
	"golang.org/x/sys/windows"

	"1loop/internal/softgl"
)

const (
	// softwareVariable asks the next process to draw with Mesa.
	softwareVariable = "ONELOOP_SOFTWARE_GL"
	// probeVariable makes a process that only checks the PC's driver.
	probeVariable = "ONELOOP_GL_PROBE"
)

// mesa says how this process draws: tried, Mesa was to draw;
// remembered, because this PC needed it before; failure, why Mesa could
// not be loaded.
var mesa struct {
	tried, remembered bool
	failure           error
}

// choicePath remembers that this PC needs Mesa.
func choicePath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "1loop", "renderer.txt")
}

func remembered() bool {
	path := choicePath()
	data, err := os.ReadFile(path)
	return path != "" && err == nil && len(data) > 0 && data[0] == 's'
}

func remember(needed bool) {
	path := choicePath()
	if path == "" {
		return
	}
	if !needed {
		os.Remove(path)
		return
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("software: this PC had no usable OpenGL driver; 1loop draws with its Mesa. Delete this file to try the driver again.\r\n"), 0o644)
}

// softwareRendererStart runs first, before GLFW starts: Mesa is loaded now
// when this PC needs it.
func softwareRendererStart() {
	if os.Getenv(probeVariable) == "1" {
		os.Exit(probeDriver())
	}
	forced := os.Getenv(softwareVariable) == "1"
	os.Unsetenv(softwareVariable) // not for what 1loop starts
	mesa.remembered = remembered()
	if !forced && !mesa.remembered {
		return
	}
	mesa.tried = true
	dir, err := softgl.Extract()
	if err == nil {
		err = softgl.Load(dir)
	}
	if err != nil {
		mesa.failure = err
		rendererLogf("Mesa not loaded: %v", err)
		return
	}
	rendererLogf("drawing with Mesa (llvmpipe) from %s", dir)
}

// windowMissing says the window could not be made: it has no handle.
func windowMissing(window fyne.Window) bool {
	missing := true
	withHWND(window, func(uintptr) { missing = false })
	return missing
}

// rendererReady runs once the window is shown: with Mesa, the PC's driver
// is checked again in the background.
func rendererReady() {
	forgetTried()
	if !mesa.tried || mesa.failure != nil {
		return
	}
	remember(true)
	if mesa.remembered {
		go recheckDriver()
	}
}

// recheckDriver starts a process that only makes a hidden OpenGL window with
// the PC's driver: when it can, the next start uses the driver.
func recheckDriver() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	command := exec.Command(exe)
	command.Env = append(os.Environ(), probeVariable+"=1")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Start(); err != nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		command.Process.Kill()
		err = errors.New("no answer in 30 s")
	}
	if err != nil {
		rendererLogf("the PC's OpenGL driver still cannot draw (%v): Mesa stays", err)
		return
	}
	rendererLogf("the PC's OpenGL driver works now: the next start uses it")
	remember(false)
}

// probeDriver makes a hidden window with OpenGL 2.1 through the PC's own
// opengl32.dll: 0 when it can.
func probeDriver() (status int) {
	windows.SetErrorMode(windows.SEM_FAILCRITICALERRORS | windows.SEM_NOGPFAULTERRORBOX)
	runtime.LockOSThread()
	defer func() {
		if recover() != nil {
			status = 1
		}
	}()
	if err := glfw.Init(); err != nil {
		return 1
	}
	defer glfw.Terminate()
	glfw.WindowHint(glfw.Visible, glfw.False)
	glfw.WindowHint(glfw.ContextVersionMajor, 2)
	glfw.WindowHint(glfw.ContextVersionMinor, 1)
	window, err := glfw.CreateWindow(64, 64, "1loop", nil, nil)
	if err != nil {
		return 1
	}
	window.Destroy()
	return 0
}

// renderFailed is the window that could not be made: 1loop starts again
// with Mesa, unless it already drew with it; then it says so. It never
// returns.
func renderFailed(failure error, release func()) {
	release()
	if !mesa.tried {
		noteTried("The PC's OpenGL driver", failure)
		err := relaunchWithMesa()
		if err == nil {
			os.Exit(0)
		}
		noteTried("1loop's own renderer, which draws without a driver (Mesa)", err)
	} else {
		if mesa.failure != nil {
			failure = mesa.failure
		}
		remember(false) // the next start tries the driver first again
		noteTried("1loop's own renderer, which draws without a driver (Mesa)", failure)
	}
	messageBox(renderFailureTitle, renderFailureText(triedSoFar(), []string{
		"Install or update the graphics driver: through Windows Update, or from the site of the PC's or the graphics card's maker. Then start 1loop again.",
		"In a virtual machine, turn on 3D acceleration in its settings, or install its guest additions.",
		"Over Remote Desktop, try starting 1loop on the PC itself once.",
	}))
	os.Exit(1)
}

// relaunchWithMesa extracts Mesa and starts this exe again drawing with it.
func relaunchWithMesa() error {
	if _, err := softgl.Extract(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(exe, os.Args[1:]...)
	command.Env = append(os.Environ(), softwareVariable+"=1")
	if err := command.Start(); err != nil {
		return err
	}
	rendererLogf("starting again with Mesa")
	return nil
}

func messageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const iconWarning = 0x30
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), iconWarning)
}
