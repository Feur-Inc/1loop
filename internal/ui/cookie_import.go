// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → Tools & extensions → Browser → Import cookies: the agents' browser logs
// in with the cookies of a browser profile on this computer (desktop only).

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/browser"
	"1loop/internal/cookieimport"
)

const cookieWarning = "Agents can act in imported accounts. Import only what you accept."

const cookieExportGuide = "1. Open the browser profile with the accounts to import.\n\n2. With a trusted cookie-export extension, export JSON for them.\n\n3. In 1loop, choose JSON file, select the export, then Import.\n\n4. Open the site in Browser Use to check the login.\n\n5. The export holds logins: keep it private and delete it after.\n\nFirefox, or Chromium on Linux, imports directly. Close it first."

func heading(text string) *canvas.Text {
	line := textLine(text, 14, colText)
	line.TextStyle = fyne.TextStyle{Bold: true}
	return line
}

func (ui *desktopUI) importCookies() {
	searching := note("Looking for browsers on this computer…")
	var waiting *sheet
	waiting = ui.modal("Import cookies", searching, 560, nil, newRow("Cancel", nil, styleOutline, func() { waiting.dismiss() }))
	go func() {
		profiles := cookieimport.Find()
		fyne.Do(func() {
			if !waiting.open {
				return
			}
			waiting.dismiss()
			ui.chooseCookieProfile(profiles)
		})
	}()
}

// chooseCookieProfile is the dialog: the warning, the profiles, and the
// user's agreement before anything is imported.
func (ui *desktopUI) chooseCookieProfile(profiles []cookieimport.Profile) {
	chosen := -1
	file := ""
	fileLabel := note("No JSON file selected.")
	agreed := false
	importing := false
	var install *tapRow
	update := func() {
		if !importing && (chosen >= 0 || file != "") && agreed {
			install.Enable()
		} else {
			install.Disable()
		}
	}
	warning := container.NewBorder(nil, nil, container.NewVBox(padded(2, 0, 0, 0, iconImage(iconShield, colAmber, 16))), nil,
		container.NewVBox(heading("Imported cookies hand your accounts to the agents"), plainFlow(cookieWarning, 13, colText)))
	list := container.NewVBox()
	var rows []*tapRow
	for i, profile := range profiles {
		i := i
		row := newRow(profile.Label(), nil, styleChip, func() {
			chosen = i
			file = ""
			fileLabel.Text = "No JSON file selected."
			fileLabel.Refresh()
			for j, row := range rows {
				row.SetSelected(j == i)
				row.SetTrailing(map[bool]string{true: "✓"}[j == i])
			}
			update()
		})
		rows = append(rows, row)
		list.Add(container.NewVBox(row, padded(0, 4, 14, 0, textLine(profile.Dir, 11.5, colFaint))))
	}
	chooseFile := newRow("Choose JSON file…", nil, styleOutline, func() {
		ui.pick(pickRequest{title: "Import cookie JSON", filterName: "Cookie JSON", extensions: []string{".json"}}, func(paths []string) {
			file, chosen = paths[0], -1
			fileLabel.Text = filepath.Base(file)
			fileLabel.Refresh()
			for _, row := range rows {
				row.SetSelected(false)
				row.SetTrailing("")
			}
			update()
		})
	})
	guide := newRow("How to export cookies", nil, styleOutline, func() {
		paragraphs := container.New(layout.NewCustomPaddedVBoxLayout(10))
		for _, paragraph := range strings.Split(cookieExportGuide, "\n\n") {
			paragraphs.Add(plainFlow(paragraph, 13, colText))
		}
		text := container.NewVScroll(padded(0, 0, 0, 12, paragraphs))
		text.SetMinSize(fyne.NewSize(0, min(float32(420), max(float32(160), ui.window.Canvas().Size().Height-180))))
		link := newRow("Cookie-Editor project", nil, styleOutline, func() { openLink("https://github.com/Moustachauve/cookie-editor") })
		ui.modal("Export and import cookies", container.NewVBox(text, link), 620, nil)
	})
	sources := container.NewVBox(sectionTitle("From a JSON export"), note("Chrome, Edge and other browsers, including protected Windows profiles."), container.NewHBox(chooseFile, guide), fileLabel)
	if len(profiles) > 0 {
		sources.Add(sectionTitle("Or from a local profile"))
		sources.Add(note("Close the source browser first. Windows Chromium needs a JSON export."))
		sources.Add(list) // the dialog scrolls; a scroll of its own here would catch the wheel
	} else {
		sources.Add(note("No compatible local profile found. Choose a JSON export instead."))
	}
	agree := settingRow("I understand", "Agents may use the accounts in these cookies.", newToggle(false, func(on bool) { agreed = on; update() }))
	body := container.NewVBox(surface(warning, colRaised, colBorder, 10, 12), sources, card(agree))
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	install = newRow("Import", nil, stylePrimary, func() {
		path := file
		var profile cookieimport.Profile
		if chosen >= 0 {
			profile = profiles[chosen]
		}
		importing = true
		install.Disable()
		cancel.Disable()
		install.SetText("Importing…")
		if ui.browser == nil {
			ui.browser = browser.NewBrowserEngine(ui.ctx)
			ui.browser.Scale = float64(ui.window.Canvas().Scale())
		}
		engine := ui.browser
		go func() {
			ctx, stop := context.WithTimeout(ui.ctx, 3*time.Minute)
			defer stop()
			var result cookieimport.Result
			var err error
			source := profile.Label()
			if path != "" {
				source = filepath.Base(path)
				result, err = cookieimport.ReadJSON(path)
			} else {
				result, err = cookieimport.Read(ctx, profile)
			}
			kept := 0
			if err == nil && len(result.Cookies) > 0 {
				kept, err = engine.ImportCookies(ctx, result.Cookies)
			}
			fyne.Do(func() {
				dialog.dismiss()
				if err != nil {
					ui.notice("Import cookies", err.Error())
					return
				}
				text := fmt.Sprintf("Imported %s from %s. Open your site in Browser Use to check it.", countText(kept, "cookie"), source)
				if len(result.Cookies) == 0 {
					text = "No usable cookies found. Try a JSON export."
				}
				if refused := len(result.Cookies) - kept; refused > 0 {
					text += " The browser refused " + countText(refused, "invalid cookie") + "."
				}
				if result.Skipped > 0 {
					verb := "were"
					if result.Skipped == 1 {
						verb = "was"
					}
					text += fmt.Sprintf(" %s %s left out (incomplete, expired or private).", countText(result.Skipped, "cookie"), verb)
				}
				ui.notice("Import cookies", text)
			})
		}()
	})
	install.Disable()
	content := container.NewVScroll(padded(0, 0, 0, 12, body)) // clear of the scroll bar
	content.SetMinSize(fyne.NewSize(0, min(float32(500), max(float32(160), ui.window.Canvas().Size().Height-180))))
	dialog = ui.modal("Import cookies", content, 680, nil, cancel, install)
}
