// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"1loop/internal/platform"
)

func clipboardReady() {}

func imageClipboardData() ([]byte, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $image=[Windows.Forms.Clipboard]::GetImage(); if($null -eq $image){exit 1}; $stream=New-Object IO.MemoryStream; $image.Save($stream,[Drawing.Imaging.ImageFormat]::Png); [Console]::Write([Convert]::ToBase64String($stream.ToArray())); $image.Dispose(); $stream.Dispose()`
	command := exec.Command("powershell.exe", "-NoProfile", "-STA", "-EncodedCommand", platform.EncodePowerShell(script))
	platform.PrepareProcess(command)
	data, err := command.Output()
	if err != nil {
		return nil, errNoClipboardImage
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
}

// copyImageToClipboard puts a PNG picture on the clipboard, as PNG (kept
// transparent, for the applications that read it) and as a bitmap.
func copyImageToClipboard(data []byte) error {
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
	path := "'" + strings.ReplaceAll(file.Name(), "'", "''") + "'"
	script := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; ` +
		`$bytes=[IO.File]::ReadAllBytes(` + path + `); $image=[Drawing.Image]::FromStream((New-Object IO.MemoryStream(,$bytes))); ` +
		`$data=New-Object Windows.Forms.DataObject; $data.SetImage($image); $data.SetData('PNG',(New-Object IO.MemoryStream(,$bytes))); ` +
		`[Windows.Forms.Clipboard]::SetDataObject($data,$true)`
	command := exec.Command("powershell.exe", "-NoProfile", "-STA", "-EncodedCommand", platform.EncodePowerShell(script))
	platform.PrepareProcess(command)
	if out, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("the picture could not be copied: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
