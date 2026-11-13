// SPDX-License-Identifier: MPL-2.0

//go:build windows

package ui

// Without an OpenGL driver (a virtual machine, some remote desktops: only
// "Microsoft Basic Display Adapter"), GLFW cannot make the window and 1loop
// would run with nothing on screen. The Windows download carries a copy in
// software\ that draws with Mesa on the CPU (packaging/windows/build.sh,
// 1loop-software.cmd): 1loop starts it by itself instead.

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"fyne.io/fyne/v2"
)

// windowMissing says the window could not be made: it has no handle.
func windowMissing(window fyne.Window) bool {
	missing := true
	withHWND(window, func(uintptr) { missing = false })
	return missing
}

// startSoftwareRenderer starts the download's CPU-rendered copy, with the
// same settings as 1loop-software.cmd, once release has let go of what the
// copy needs (the one-instance claim) and noted a clean end. Without that
// copy (the exe alone, out of the download), it says what is missing.
// Either way this process has nothing left to show and should end: true.
func startSoftwareRenderer(release func()) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	dir := filepath.Dir(exe)
	software := filepath.Join(dir, "software", "1loop.exe")
	release()
	if _, err := os.Stat(filepath.Join(dir, "software", "opengl32.dll")); err != nil || filepath.Base(dir) == "software" {
		messageBox("1loop cannot open its window",
			"This PC has no OpenGL driver, which 1loop draws with (a virtual machine or a remote desktop often has none).\n\n"+
				"Start 1loop from the full Windows download: its software folder draws without a driver.")
		return true
	}
	command := exec.Command(software, os.Args[1:]...)
	command.Env = append(os.Environ(), "GALLIUM_DRIVER=llvmpipe", "LP_NUM_THREADS=1")
	if err := command.Start(); err != nil {
		messageBox("1loop cannot open its window", "This PC has no OpenGL driver, and the copy that draws without one did not start:\n"+err.Error())
	}
	return true
}

func messageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	const iconWarning = 0x30
	user32.NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), iconWarning)
}
