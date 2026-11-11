// SPDX-License-Identifier: MPL-2.0

package ui

// After a crash, the next launch offers to report it (internal/crash): the
// whole anonymized report on screen, then a GitHub link the user opens and
// submits from their own account. 1loop sends nothing itself.

import (
	"net/url"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/crash"
	"1loop/internal/platform"
)

// watchCrashes starts watching this run, and returns the last one's crash
// to offer once the window is up. Before the developer log starts, which
// moves the last session's log away.
func watchCrashes(statePath string) *crash.Crash {
	env := crash.Environment("desktop")
	env.Backend = backendLabel()
	devLog := ""
	if platform.DevWanted {
		devLog = platform.DevLogPath()
	}
	return crash.Start(filepath.Dir(statePath), env, devLog)
}

// offerCrashReport shows the last run's crash, once.
func (ui *desktopUI) offerCrashReport() {
	last := ui.lastCrash
	ui.lastCrash = nil
	if last == nil || ui.crashReportsOff {
		return
	}
	platform.Devf("app", "the last run crashed: offering its report")
	report := crash.NewReport(*last, crash.NewScrubber())
	withLog := false
	well := container.NewStack()
	note := container.NewVBox()
	show := func() {
		well.Objects = []fyne.CanvasObject{codeBlock(report.Text(withLog), 300)}
		well.Refresh()
		note.Objects = nil
		if _, cut := report.URL(withLog); cut {
			note.Add(plainFlow("Trace cut to fit the link; Copy report has all of it.", 12.5, colMuted))
		}
		note.Refresh()
	}
	show()
	body := container.New(layout.NewCustomPaddedVBoxLayout(10), well, note)
	if report.HasLog() {
		body.Add(settingRow("Add the developer log", "",
			newToggle(false, func(on bool) { withLog = on; show() })))
	}
	var dialog *sheet
	never := newRow("Don't ask again", nil, styleChip, func() {
		dialog.Hide()
		ui.crashReportsOff = true
		ui.save()
	})
	later := newRow("Not now", nil, styleOutline, func() { dialog.Hide() })
	copyReport := newRow("Copy report", nil, styleOutline, func() {
		fyne.CurrentApp().Clipboard().SetContent(report.Text(withLog))
	})
	file := newRow("Report on GitHub", nil, stylePrimary, func() {
		link, _ := report.URL(withLog)
		target, err := url.Parse(link)
		if err == nil {
			err = fyne.CurrentApp().OpenURL(target)
		}
		if err != nil {
			fyne.CurrentApp().Clipboard().SetContent(report.Text(withLog))
			ui.notice("Report on GitHub", "The browser could not be opened: "+err.Error()+"\n\nThe report was copied instead; paste it into a new issue at "+crash.NewIssue+".")
			return
		}
		dialog.Hide()
	})
	dialog = ui.modal("1loop closed unexpectedly", body, 640, nil, never, later, copyReport, file)
}
