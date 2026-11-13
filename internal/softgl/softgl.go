// SPDX-License-Identifier: MPL-2.0

// Package softgl carries Mesa's OpenGL, drawn on the CPU, inside the Windows
// build (-tags mesa, packaging/windows/build.sh), for PCs without an OpenGL
// driver: a virtual machine, some remote desktops. 1loop.exe links
// opengl32.dll, which Windows looks for first beside the exe: Install puts
// Mesa's there, and 1loop starts again with it (internal/ui
// software_gl_windows.go). Nothing is installed on the system.
package softgl

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

// marker says the DLLs beside the exe are the ones 1loop put there.
const marker = "1loop-software-gl.txt"

// Available says this build carries Mesa.
func Available() bool { return len(files()) > 0 }

// Installed says Mesa is beside the exe in dir, put there by 1loop.
func Installed(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, marker)); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "opengl32.dll"))
	return err == nil
}

// Install writes Mesa's DLLs beside the exe in dir, each through a temporary
// file, then the marker.
func Install(dir string) error {
	for name, data := range files() {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		temporary := filepath.Join(dir, name+".1loop-tmp")
		file, err := os.Create(temporary)
		if err != nil {
			return err
		}
		_, err = io.Copy(file, reader)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(temporary, filepath.Join(dir, name))
		}
		if err != nil {
			os.Remove(temporary)
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, marker), []byte("1loop put opengl32.dll and libgallium_wgl.dll here (Mesa, MIT licence): this PC had no OpenGL driver. Delete the three files to stop using them.\r\n"), 0o644)
}
