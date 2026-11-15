// SPDX-License-Identifier: MPL-2.0

//go:build windows

package softgl

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Load makes Mesa, from dir, the process's opengl32.dll, drawing with
// llvmpipe. It must run before GLFW starts: GLFW and go-gl then load
// opengl32.dll by name and get this one. Mesa reads its driver as it
// loads, from the process's environment.
func Load(dir string) error {
	if os.Getenv("GALLIUM_DRIVER") == "" {
		os.Setenv("GALLIUM_DRIVER", "llvmpipe")
	}
	for _, name := range names() {
		// The altered search path finds libgallium_wgl.dll beside
		// opengl32.dll; gallium is loaded first anyway.
		if _, err := windows.LoadLibraryEx(filepath.Join(dir, name), 0, windows.LOAD_WITH_ALTERED_SEARCH_PATH); err != nil {
			return &os.PathError{Op: "load", Path: filepath.Join(dir, name), Err: err}
		}
	}
	return nil
}
