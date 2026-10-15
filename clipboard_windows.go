//go:build !server

package main

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"
)

func imageClipboardData() ([]byte, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms; Add-Type -AssemblyName System.Drawing; $image=[Windows.Forms.Clipboard]::GetImage(); if($null -eq $image){exit 1}; $stream=New-Object IO.MemoryStream; $image.Save($stream,[Drawing.Imaging.ImageFormat]::Png); [Console]::Write([Convert]::ToBase64String($stream.ToArray())); $image.Dispose(); $stream.Dispose()`
	command := exec.Command("powershell.exe", "-NoProfile", "-STA", "-EncodedCommand", encodePowerShell(script))
	prepareProcess(command)
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("No image in the clipboard")
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
}
