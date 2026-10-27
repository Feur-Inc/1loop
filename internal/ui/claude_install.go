package ui

import (
	"context"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
)

// setFlow replaces a flowText's words.
func setFlow(flow *flowText, text string) {
	flow.spans, flow.cut = []span{{text: text}}, nil
	flow.flow(flow.Size().Width)
	flow.Refresh()
}

// installClaudeCode asks before running Claude Code's official installer,
// then shows how far it is; done runs once Claude Code is installed.
func (ui *desktopUI) installClaudeCode(done func()) {
	home, _ := os.UserHomeDir()
	paragraphs := []string{
		"Claude Code is not installed on this computer, and Claude Code connections need it. 1loop can install it with Anthropic's official installer, as its install script does:",
		"1. Download the latest Claude Code from downloads.claude.ai and check it against the SHA-256 checksum of the release's manifest.",
		"2. Run its claude install, which puts Claude Code in " + filepath.Join(home, ".local", "bin") + " and its data in " + filepath.Join(home, ".claude") + ", for your user only. No administrator rights are needed.",
		"Claude Code then uses its own sign-in: your Claude subscription.",
	}
	body := container.New(layout.NewCustomPaddedVBoxLayout(10))
	for _, text := range paragraphs {
		body.Add(plainFlow(text, 13, colText))
	}
	status := plainFlow("", 12.5, colMuted)
	body.Add(status)
	ctx, stop := context.WithCancel(ui.ctx)
	var dialog *sheet
	cancel := newRow("Not now", nil, styleOutline, func() { dialog.dismiss() })
	install := newRow("Install", nil, stylePrimary, nil)
	install.onTap = func() {
		install.Disable()
		install.SetText("Installing…")
		go func() {
			path, err := agent.InstallClaudeCode(ctx, func(text string) { fyne.Do(func() { setFlow(status, text) }) })
			fyne.Do(func() {
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					setFlow(status, err.Error())
					install.SetText("Try again")
					install.Enable()
					return
				}
				dialog.dismiss()
				done()
				ui.notice("Claude Code installed", "Claude Code is installed in "+path+". If it was never signed in on this computer, run claude once in a terminal to sign in.")
			})
		}()
	}
	dialog = ui.modal("Install Claude Code?", body, 560, stop, cancel, install)
}
