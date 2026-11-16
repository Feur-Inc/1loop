// SPDX-License-Identifier: MPL-2.0

//go:build !windows && !linux

package ui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Elsewhere (macOS, the BSDs) 1loop asks the clipboard tools.
func clipboardReady() {}

func imageClipboardData() ([]byte, error) { return externalImageClipboardData() }

// copyImageToClipboard puts a PNG picture on the clipboard: through
// AppleScript on macOS, xclip on the BSDs.
func copyImageToClipboard(data []byte) error {
	if runtime.GOOS != "darwin" {
		command := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-i")
		command.Stdin = bytes.NewReader(data)
		if err := command.Run(); err != nil {
			return clipboardToolError("xclip", err)
		}
		return nil
	}
	file, err := os.CreateTemp("", "1loop-picture-*.png")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	path := `"` + strings.ReplaceAll(strings.ReplaceAll(file.Name(), `\`, `\\`), `"`, `\"`) + `"`
	script := `set the clipboard to (read (POSIX file ` + path + `) as «class PNGf»)`
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("the picture could not be copied: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
