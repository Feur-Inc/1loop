package ui

// Updates of the desktop app from the release page (internal/update): a
// check at start and every six hours, a notification and a sidebar row when
// a version is out, and Settings → Updates to install it and restart.

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/platform"
	"1loop/internal/update"
)

type updateState struct {
	status    update.Status
	manifest  update.Manifest
	checked   time.Time
	checking  bool
	notified  string // the version already announced
	installer bool   // an installation is running
}

// watchUpdates checks shortly after start, then every six hours.
func (ui *desktopUI) watchUpdates() {
	go func() {
		select {
		case <-ui.ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			fyne.Do(func() { ui.checkUpdates(nil) })
			select {
			case <-ui.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// checkUpdates reads the release page; done runs on the window's thread.
func (ui *desktopUI) checkUpdates(done func()) {
	if ui.updates.checking {
		return
	}
	ui.updates.checking = true
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
		defer cancel()
		status, manifest := update.Check(ctx, "desktop")
		fyne.Do(func() {
			ui.updates.checking = false
			ui.updates.status, ui.updates.manifest, ui.updates.checked = status, manifest, time.Now()
			platform.Devf("update", "current %s latest %s available %v problem %q", status.Current, status.Latest, status.Available, status.Problem)
			if status.Available && ui.updates.notified != status.Latest {
				ui.updates.notified = status.Latest
				fyne.CurrentApp().SendNotification(fyne.NewNotification("1loop "+status.Latest+" is available", "Settings → Updates installs it and restarts 1loop."))
				var prompt *sheet
				later := newRow("Later", nil, styleOutline, func() { prompt.dismiss() })
				view := newRow("View update", nil, stylePrimary, func() {
					prompt.dismiss()
					ui.settings()
					ui.prefs.show("Updates")
				})
				prompt = ui.modal("1loop "+status.Latest+" is available", note("Review the release notes and install the update when you are ready. Your chats and settings stay."), 520, nil, later, view)
			}
			ui.showUpdateRow()
			if done != nil {
				done()
			}
		})
	}()
}

// showUpdateRow shows the sidebar's "Update available" row when there is one.
func (ui *desktopUI) showUpdateRow() {
	if ui.updateRow == nil {
		return
	}
	if ui.updates.status.Available {
		ui.updateRow.SetText("Update available · " + ui.updates.status.Latest)
		ui.updateRow.Show()
	} else {
		ui.updateRow.Hide()
	}
	if side, ok := ui.sideApp.(*fyne.Container); ok {
		relayout(side) // Show alone leaves the row at its hidden, empty size
	}
}

// updateSettings is Settings → Updates.
func (ui *desktopUI) updateSettings() fyne.CanvasObject {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	notes := newMarkdownView("")
	box := container.NewVBox()
	var render func()
	check := newRow("Check now", nil, styleOutline, nil)
	install := newRow("Update and restart", nil, stylePrimary, nil)
	render = func() {
		state := ui.updates.status
		text := "This is 1loop " + update.Version + "."
		switch {
		case ui.updates.checking:
			text += " Checking the release page…"
		case ui.updates.checked.IsZero():
			text += " Not checked yet."
		case state.Available:
			text += fmt.Sprintf(" Version %s is available.", state.Latest)
		case state.Latest != "":
			text += " It is up to date."
		}
		if state.Problem != "" {
			text += "\n" + state.Problem
		}
		if !ui.updates.checked.IsZero() {
			text += "\nLast checked " + ui.updates.checked.Format("15:04") + "."
		}
		status.SetText(text)
		notes.ParseMarkdown(state.Notes)
		if state.CanUpdate && !ui.updates.installer {
			install.Show()
		} else {
			install.Hide()
		}
		if ui.updates.installer {
			check.Disable()
		} else {
			check.Enable()
		}
	}
	check.onTap = func() { ui.checkUpdates(render); render() }
	install.onTap = func() {
		ui.updates.installer = true
		status.SetText("Downloading and checking 1loop " + ui.updates.status.Latest + "…")
		render()
		manifest := ui.updates.manifest
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Minute)
			defer cancel()
			exe, err := update.Install(ctx, manifest, "desktop")
			fyne.Do(func() {
				if err != nil {
					ui.updates.installer = false
					ui.updates.status.Problem = err.Error()
					render()
					return
				}
				ui.restartInto(exe) // the new version starts in place of this one
			})
		}()
	}
	box.Add(card(settingStack("1loop updates", "1loop checks its release page when it starts and every six hours, and tells you when a new version is out. An update replaces 1loop's files in their folder, keeping the previous ones as .old, then restarts 1loop. Downloads are checked against the release's signature before anything is replaced.",
		container.NewVBox(status, container.NewHBox(check, install)))))
	box.Add(notes.box)
	render()
	if ui.updates.checked.IsZero() {
		ui.checkUpdates(render)
	}
	return box
}
