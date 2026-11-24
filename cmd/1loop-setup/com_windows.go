// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The two Windows objects the setup needs, called through their tables:
// shell links, and the folder picker.

var (
	ole32            = windows.NewLazySystemDLL("ole32.dll")
	coCreateInstance = ole32.NewProc("CoCreateInstance")
	coTaskMemFree    = ole32.NewProc("CoTaskMemFree")
	shell32          = windows.NewLazySystemDLL("shell32.dll")
	itemFromName     = shell32.NewProc("SHCreateItemFromParsingName")

	clsidShellLink   = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLink     = windows.GUID{Data1: 0x000214F9, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile   = windows.GUID{Data1: 0x0000010B, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	clsidFileOpen    = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpen      = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidShellItem     = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
	sigdnFileSysPath = uintptr(0x80058000)
)

// object is a COM object: its first word is its table of functions.
type object struct{ table *[32]uintptr }

func (o *object) call(index int, args ...uintptr) error {
	result, _, _ := syscall.SyscallN(o.table[index], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	if int32(result) < 0 {
		return syscall.Errno(uint32(result))
	}
	return nil
}

func (o *object) release() { o.call(2) }

func create(class, iid *windows.GUID) (*object, error) {
	windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED) // already done is fine
	var made *object
	result, _, _ := coCreateInstance.Call(uintptr(unsafe.Pointer(class)), 0, 1, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&made)))
	if int32(result) < 0 || made == nil {
		return nil, syscall.Errno(uint32(result))
	}
	return made, nil
}

// widened keeps the strings given to Windows for as long as the setup runs:
// as numbers, the collector does not see them used.
var (
	widened   []*uint16
	widenedMu sync.Mutex
)

func wide(text string) uintptr {
	pointer, err := windows.UTF16PtrFromString(text)
	if err != nil {
		pointer = new(uint16)
	}
	widenedMu.Lock()
	widened = append(widened, pointer)
	widenedMu.Unlock()
	return uintptr(unsafe.Pointer(pointer))
}

// shortcut writes a .lnk that starts target.
func shortcut(path, target string) error {
	link, err := create(&clsidShellLink, &iidShellLink)
	if err != nil {
		return fmt.Errorf("Cannot make the shortcut: %w", err)
	}
	defer link.release()
	home, _ := os.UserHomeDir()
	link.call(7, wide("1loop"))
	link.call(9, wide(home))
	link.call(17, wide(target), 0)
	if err := link.call(20, wide(target)); err != nil {
		return fmt.Errorf("Cannot make the shortcut: %w", err)
	}
	var file *object
	if err := link.call(0, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&file))); err != nil {
		return fmt.Errorf("Cannot make the shortcut: %w", err)
	}
	defer file.release()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := file.call(6, wide(path), 1); err != nil {
		return fmt.Errorf("Cannot write %s: %w", path, err)
	}
	return nil
}

// pickFolder asks for a folder, "" when the user cancels.
func pickFolder(owner windows.HWND, from string) string {
	dialog, err := create(&clsidFileOpen, &iidFileOpen)
	if err != nil {
		return ""
	}
	defer dialog.release()
	dialog.call(9, 0x20|0x40) // folders, of the file system
	dialog.call(17, wide("Where to install 1loop"))
	dialog.call(18, wide("Install here"))
	for from != filepath.Dir(from) { // the nearest folder that exists
		if _, err := os.Stat(from); err == nil {
			break
		}
		from = filepath.Dir(from)
	}
	var start *object
	if result, _, _ := itemFromName.Call(wide(from), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&start))); int32(result) >= 0 && start != nil {
		dialog.call(12, uintptr(unsafe.Pointer(start)))
		start.release()
	}
	if dialog.call(3, uintptr(owner)) != nil {
		return ""
	}
	var item *object
	if dialog.call(20, uintptr(unsafe.Pointer(&item))) != nil {
		return ""
	}
	defer item.release()
	var name *uint16
	if item.call(5, sigdnFileSysPath, uintptr(unsafe.Pointer(&name))) != nil {
		return ""
	}
	defer coTaskMemFree.Call(uintptr(unsafe.Pointer(name)))
	return windows.UTF16PtrToString(name)
}
