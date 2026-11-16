// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"os/exec"
	"strings"

	"1loop/internal/platform"
)

// windowsSavePick asks Windows' own Save As dialog, through PowerShell.
func windowsSavePick(pickRequestValue pickRequest) ([]string, error) {
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
	filter := "All files (*.*)|*.*"
	if len(pickRequestValue.extensions) > 0 {
		patterns := strings.ReplaceAll(globs(pickRequestValue.extensions), " ", ";")
		filter = pickRequestValue.filterName + " (" + patterns + ")|" + patterns + "|" + filter
	}
	script := `Add-Type -AssemblyName System.Windows.Forms; $dialog=New-Object Windows.Forms.SaveFileDialog; ` +
		`$dialog.Title=` + quote(pickRequestValue.title) + `; $dialog.FileName=` + quote(pickRequestValue.name) + `; ` +
		`$dialog.Filter=` + quote(filter) + `; $dialog.OverwritePrompt=$true; `
	if pickRequestValue.start != "" {
		script += `$dialog.InitialDirectory=` + quote(pickRequestValue.start) + `; `
	}
	script += `if($dialog.ShowDialog() -eq 'OK'){[Console]::Write($dialog.FileName)}`
	command := exec.Command("powershell.exe", "-NoProfile", "-STA", "-EncodedCommand", platform.EncodePowerShell(script))
	platform.PrepareProcess(command)
	out, err := command.Output()
	if err != nil {
		return nil, errNoNativePicker // no PowerShell: Fyne's dialog
	}
	if path := strings.TrimSpace(string(out)); path != "" {
		return []string{path}, nil
	}
	return nil, nil
}
