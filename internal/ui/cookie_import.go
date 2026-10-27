package ui

// Settings → General → Browser → Import cookies: the agents' browser logs
// in with the cookies of a browser profile on this computer (desktop only).

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"

	"1loop/internal/browser"
	"1loop/internal/cookieimport"
)

const cookieWarning = "Imported cookies can give agents access to your accounts, including ones you did not intend to share: mail, banking, shopping, work or social networks. An agent could send messages, buy, delete or change settings using those accounts. Mistakes and malicious pages can cause harmful actions. Import only accounts you accept to give agents access to. Clear browsing data removes the browser's local sessions; it does not undo actions already performed."

const cookieExportGuide = "1. Open the browser and profile containing the accounts you want to import. Use a dedicated profile or export only the sites you need.\n\n2. With a cookie-export extension you trust (for example Cookie-Editor), explicitly allow access to those sites, then choose Export and JSON. If it copies JSON to the clipboard, paste it into a local text editor and save cookies.json. Chrome and Edge do not provide a built-in cookie-file export. Modern Windows profiles cannot be imported by copying their encrypted cookie database.\n\n3. In 1loop, choose JSON file, select the export, read the warning, enable I understand, then click Import. JSON arrays and objects containing a cookies array are supported.\n\n4. Open the site in Browser Use to check the connection. Some sites require signing in again, and an imported cookie does not guarantee a working login. Session cookies may expire when the browser closes.\n\n5. The export contains sensitive session data. Keep it private and remove it yourself when no longer needed. 1loop reads this file locally without deleting or changing it. Disable or remove the export extension if you no longer need it.\n\nDirect profile import is also available for Firefox-family browsers on Windows and Linux, and compatible Chromium-family browsers on Linux. Close the source browser first to obtain a consistent copy. Private, container and partitioned cookies are skipped."

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
			}
			update()
		})
		rows = append(rows, row)
		list.Add(container.NewVBox(row, padded(0, 4, 14, 0, textLine(profile.Dir, 11.5, colFaint))))
	}
	scroll := container.NewVScroll(list)
	scroll.SetMinSize(fyne.NewSize(0, min(float32(len(profiles))*58, 260)))
	chooseFile := newRow("Choose JSON file…", nil, styleOutline, func() {
		ui.pick(pickRequest{title: "Import cookie JSON", filterName: "Cookie JSON", extensions: []string{".json"}}, func(paths []string) {
			file, chosen = paths[0], -1
			fileLabel.Text = filepath.Base(file)
			fileLabel.Refresh()
			for _, row := range rows {
				row.SetSelected(false)
			}
			update()
		})
	})
	guide := newRow("How to export cookies", nil, styleOutline, func() {
		text := container.NewVScroll(plainFlow(cookieExportGuide, 13, colText))
		text.SetMinSize(fyne.NewSize(0, min(float32(420), max(float32(160), ui.window.Canvas().Size().Height-180))))
		link := newRow("Cookie-Editor project", nil, styleOutline, func() { openLink("https://github.com/Moustachauve/cookie-editor") })
		ui.modal("Export and import cookies", container.NewVBox(text, link), 620, nil)
	})
	sources := container.NewVBox(sectionTitle("From a JSON export"), note("Chrome, Edge and other browsers, including protected Windows profiles."), container.NewHBox(chooseFile, guide), fileLabel)
	if len(profiles) > 0 {
		sources.Add(sectionTitle("Or from a local profile"))
		sources.Add(note("Close the source browser before importing. Protected Windows Chromium profiles require a JSON export."))
		sources.Add(scroll)
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
				text := fmt.Sprintf("Imported %s from %s into the agents' browser. Open your site in Browser Use to check whether it is signed in.", countText(kept, "cookie"), source)
				if len(result.Cookies) == 0 {
					text = "No usable cookies were found. Try a JSON export from the source browser."
				}
				if refused := len(result.Cookies) - kept; refused > 0 {
					text += fmt.Sprintf(" The browser refused %d.", refused)
				}
				if result.Skipped > 0 {
					text += fmt.Sprintf(" %d were left out: invalid, expired, private, partitioned or unavailable session cookies.", result.Skipped)
				}
				ui.notice("Import cookies", text)
			})
		}()
	})
	install.Disable()
	content := container.NewVScroll(body)
	content.SetMinSize(fyne.NewSize(0, min(float32(500), max(float32(160), ui.window.Canvas().Size().Height-180))))
	dialog = ui.modal("Import cookies", content, 680, nil, cancel, install)
}
