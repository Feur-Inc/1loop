// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"testing"

	"1loop/internal/serversetup"
)

// tapChoice taps the n-th card to click (setupChoice) of a dialog.
func tapChoice(t *testing.T, dialog *sheet, n int) {
	t.Helper()
	i := 0
	for _, row := range tapRows(dialog.content) {
		if row.text == "" && row.icon == iconChevronRight {
			if i == n {
				row.onTap()
				return
			}
			i++
		}
	}
	t.Fatalf("no choice %d in the dialog", n)
}

// Connect a server asks first whether one runs (#443): yes opens the address
// form; no lists the four ways to run one, each with its guide, whose
// commands have a Copy button and whose last button opens the address form.
func TestConnectServerGuidesTheSetup(t *testing.T) {
	ui := remoteSettingsUI(t)
	connected := 0
	connect := func() { connected++ }

	ui.serverSetup("Connect a server", connect)
	tapChoice(t, topSheet(t, ui), 0) // I have a server
	if connected != 1 {
		t.Fatalf("I have a server opened the address form %d times", connected)
	}

	for n, method := range serversetup.Methods() {
		ui.serverSetup("Connect a server", connect)
		tapChoice(t, topSheet(t, ui), 1) // Set one up
		methods := topSheet(t, ui)
		tapChoice(t, methods, n)
		guide := topSheet(t, ui)
		if guide == methods {
			t.Fatalf("%s: no guide opened", method.ID)
		}
		copies, commands := 0, 0
		for _, row := range tapRows(guide.content) {
			if row.text == "Copy" {
				copies++
			}
		}
		for _, step := range method.Steps {
			if step.Command != "" {
				commands++
			}
		}
		if copies != commands {
			t.Errorf("%s: %d Copy buttons for %d commands", method.ID, copies, commands)
		}
		tapText(t, guide.content, "It runs: connect")
		if connected != n+2 {
			t.Fatalf("%s: It runs: connect did not open the address form", method.ID)
		}
	}
}

// The Remote page's Connect a server is one line and its button: the steps
// moved to the guide.
func TestConnectServerCardIsOneLine(t *testing.T) {
	ui := remoteSettingsUI(t)
	page := ui.remoteSettings()
	for _, row := range tapRows(page) {
		if row.text == "Connect a server…" {
			return
		}
	}
	t.Fatal("Remote has no Connect a server… button")
}
