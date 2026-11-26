// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/remoteapi"
)

// remoteSettingsUI is sidebarUI in a window, with its Settings page built.
func remoteSettingsUI(t *testing.T) *desktopUI {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ui := sidebarUI(t)
	ui.window = test.NewWindow(widget.NewLabel(""))
	ui.window.Resize(fyne.NewSize(1000, 800))
	t.Cleanup(ui.window.Close)
	ui.buildSettings()
	ui.prefs.open = true
	return ui
}

// tapRows are the buttons and rows an object is made of, top to bottom.
func tapRows(object fyne.CanvasObject) []*tapRow {
	var rows []*tapRow
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch object := object.(type) {
		case *tapRow:
			rows = append(rows, object)
		case *fyne.Container:
			for _, child := range object.Objects {
				walk(child)
			}
		case fyne.Widget:
			for _, child := range test.WidgetRenderer(object).Objects() {
				walk(child)
			}
		}
	}
	walk(object)
	return rows
}

// topSheet is the dialog on top of the window.
func topSheet(t *testing.T, ui *desktopUI) *sheet {
	t.Helper()
	dialog, ok := ui.window.Canvas().Overlays().Top().(*sheet)
	if !ok {
		t.Fatal("no dialog is open")
	}
	return dialog
}

// The pages of earlier versions open Remote, where their settings went
// (#443), and Remote holds every remote setting in its three parts.
func TestRemotePageHoldsTheOldPages(t *testing.T) {
	ui := remoteSettingsUI(t)
	for _, old := range []string{"Phone", "Phone remote", "SSH machines", "Remote server", "Remote"} {
		ui.prefs.showNow(old)
		if ui.prefs.page != "Remote" || ui.prefs.section != "Remote" {
			t.Errorf("%q opens %s / %s, want Remote", old, ui.prefs.section, ui.prefs.page)
		}
	}
	for _, key := range []string{"general.target", "general.ssh", "general.remote_server", "phone.pairing", "phone.remote"} {
		if ui.prefs.anchors[key] == nil {
			t.Errorf("%s is not on the Remote page", key)
		}
	}
	if _, ok := ui.prefs.rows["Phone"]; ok {
		t.Error("the sidebar still has a Phone section")
	}
	if _, ok := ui.prefs.rows["Remote"]; !ok {
		t.Error("the sidebar has no Remote section")
	}
	// The search opens Remote on the paired devices.
	ui.prefs.jump("phone.pairing")
	if ui.prefs.page != "Remote" || ui.prefs.anchors["phone.pairing"] == nil {
		t.Errorf("the search opened %s", ui.prefs.page)
	}
}

// Forget on a paired phone asks first: Cancel keeps it, Forget unpairs it
// (#443).
func TestForgetPhoneAsksFirst(t *testing.T) {
	ui := remoteSettingsUI(t)
	pixel := remoteapi.PairedDevice{Name: "Pixel", TokenHash: "aa", Added: time.Now()}
	tablet := remoteapi.PairedDevice{Name: "Tablet", TokenHash: "bb", Added: time.Now()}
	ui.devices = []remoteapi.PairedDevice{pixel, tablet}
	ui.prefs.showNow("Remote")
	page := ui.prefs.frames[0].content

	tapText(t, page, "Forget") // Pixel's
	if len(ui.devices) != 2 {
		t.Fatal("Forget unpaired the phone without asking")
	}
	tapText(t, topSheet(t, ui).content, "Cancel")
	if len(ui.devices) != 2 {
		t.Fatal("Cancel unpaired the phone")
	}

	tapText(t, page, "Forget")
	dialog := topSheet(t, ui)
	if got := tapRows(dialog.content); !slices.ContainsFunc(got, func(row *tapRow) bool { return row.text == "Cancel" }) {
		t.Fatal("the dialog has no Cancel")
	}
	tapText(t, dialog.content, "Forget")
	if !slices.Equal(ui.devices, []remoteapi.PairedDevice{tablet}) {
		t.Fatalf("after Forget: %v, want only the tablet", ui.devices)
	}
}

// Unlinking a server in the browser client asks first (#443).
func TestUnlinkServerAsksFirst(t *testing.T) {
	ui := remoteSettingsUI(t)
	ui.home = "https://home.lan:47101"
	other := serverAddress{URL: "https://other.lan:47101", Token: "o"}
	if err := saveServerAddress(other); err != nil {
		t.Fatal(err)
	}
	ui.askUnlink(other.URL)
	if got := loadServerAddresses(); len(got) != 1 {
		t.Fatalf("unlinked without asking: %v", got)
	}
	tapText(t, topSheet(t, ui).content, "Unlink")
	if got := loadServerAddresses(); len(got) != 0 {
		t.Fatalf("still linked after Unlink: %v", got)
	}
}
