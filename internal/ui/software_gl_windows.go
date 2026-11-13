// SPDX-License-Identifier: MPL-2.0

//go:build windows

package ui

// Without an OpenGL driver (a virtual machine, some remote desktops: only
// "Microsoft Basic Display Adapter"), GLFW cannot make the window and 1loop
// would run with nothing on screen. The release carries Mesa, which draws on
// the CPU (internal/softgl): 1loop puts it beside itself and starts again,
// with nothing for the user to do.

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"fyne.io/fyne/v2"

	"1loop/internal/softgl"
)

// Mesa reads its driver once, as it loads with the exe: llvmpipe, its CPU
// renderer, must be in the environment then. Without it Mesa picks a driver
// this PC lacks and 1loop ends.
const softwareDriver = "GALLIUM_DRIVER"

// softwareRendererStart runs first: with Mesa beside the exe and no driver
// chosen yet, 1loop starts again with llvmpipe, before anything is shown.
func softwareRendererStart() {
	dir, ok := exeDir()
	if !ok || !softgl.Installed(dir) || os.Getenv(softwareDriver) != "" {
		return
	}
	if relaunchSoftware() == nil {
		os.Exit(0)
	}
}

// windowMissing says the window could not be made: it has no handle.
func windowMissing(window fyne.Window) bool {
	missing := true
	withHWND(window, func(uintptr) { missing = false })
	return missing
}

// startSoftwareRenderer puts Mesa beside the exe and starts 1loop again
// with it, once release has let go of what the next one needs (the
// one-instance claim) and noted a clean end. When it cannot, it says why.
// Either way this process has nothing left to show and should end: true.
func startSoftwareRenderer(release func()) bool {
	dir, ok := exeDir()
	if !ok {
		return false
	}
	release()
	const title = "1loop cannot open its window"
	switch {
	case softgl.Installed(dir):
		// Already drawn by Mesa, and still no window.
		messageBox(title, "This PC has no OpenGL driver, and the renderer that draws without one failed too.")
		return true
	case !softgl.Available():
		messageBox(title, "This PC has no OpenGL driver, which 1loop draws with (a virtual machine or a remote desktop often has none). This build of 1loop does not carry the renderer that draws without one: use the release from the download page.")
		return true
	}
	if err := softgl.Install(dir); err != nil {
		messageBox(title, "This PC has no OpenGL driver, and 1loop could not put the renderer that draws without one in its folder:\n"+err.Error()+"\n\nMove the 1loop folder somewhere you can write, such as your user folder.")
		return true
	}
	if err := relaunchSoftware(); err != nil {
		messageBox(title, "1loop could not start again with the renderer that draws without a driver:\n"+err.Error())
	}
	return true
}

// relaunchSoftware starts this exe again, Mesa drawing on the CPU.
func relaunchSoftware() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(exe, os.Args[1:]...)
	command.Env = append(os.Environ(), softwareDriver+"=llvmpipe")
	return command.Start()
}

func exeDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return filepath.Dir(exe), true
}

func messageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const iconWarning = 0x30
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), iconWarning)
}
