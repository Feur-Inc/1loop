// SPDX-License-Identifier: MPL-2.0

package ui

// Connect a server, step by step (#443): it asks first whether a server
// already runs. One that does is connected by its address; otherwise the
// user picks how to run one (Docker, Docker Compose, the Linux or Windows
// program) and follows a short guide, each command with its Copy button,
// then connects it. The desktop's Connect a server and the browser's Link a
// server both start here; the guides are internal/serversetup's.

import (
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/serversetup"
)

// setupChoice is a card to click: a title, a line under it and a chevron.
func setupChoice(title, detail string, onTap func()) fyne.CanvasObject {
	chevron := newRow("", iconChevronRight, styleChip, onTap)
	return container.NewStack(card(settingRow(title, detail, chevron)), newTapArea(onTap))
}

// serverSetup asks whether a server already runs: connect opens the address
// form, for one that does.
func (ui *desktopUI) serverSetup(title string, connect func()) {
	var sheet *sheet
	have := setupChoice("I have a server", "Connect it by its address.", func() {
		sheet.dismiss()
		connect()
	})
	setUp := setupChoice("Set one up", "With Docker, or the server program for Linux or Windows.", func() {
		sheet.dismiss()
		ui.serverSetupMethods(title, connect)
	})
	sheet = ui.modal(title, container.New(layout.NewCustomPaddedVBoxLayout(10),
		note("A 1loop server runs the agent and your projects on another machine. Does one run already?"), have, setUp), 560, nil)
}

// serverSetupMethods lets the user pick how to run a server.
func (ui *desktopUI) serverSetupMethods(title string, connect func()) {
	var sheet *sheet
	choices := container.New(layout.NewCustomPaddedVBoxLayout(10), note("How do you want to run it?"))
	for _, method := range serversetup.Methods() {
		choices.Add(setupChoice(method.Title, method.Detail, func() {
			sheet.dismiss()
			ui.serverSetupGuide(title, method, connect)
		}))
	}
	back := newRow("Back", nil, styleOutline, func() {
		sheet.dismiss()
		ui.serverSetup(title, connect)
	})
	sheet = ui.modal(title, choices, 560, nil, back)
}

// serverSetupGuide shows one method's steps; Connect opens the address form
// once the server runs.
func (ui *desktopUI) serverSetupGuide(title string, method serversetup.Method, connect func()) {
	var sheet *sheet
	steps := container.New(layout.NewCustomPaddedVBoxLayout(14))
	for i, step := range method.Steps {
		number := textLine(strconv.Itoa(i+1)+".", 13, colAmber)
		parts := container.New(layout.NewCustomPaddedVBoxLayout(6), plainFlow(step.Text, 13, colText))
		if step.Command != "" {
			command := step.Command
			// The whole command, wrapped: the guide scrolls, not the command.
			parts.Add(surface(codeText(command), colCode, colBorderSoft, 10, 4))
			parts.Add(container.NewHBox(copyChip(func() string { return command })))
		}
		if step.Link != nil {
			link := *step.Link
			parts.Add(container.NewHBox(newRow(link.Label, iconChevronRight, styleChip, func() { openLink(link.URL) })))
		}
		// The number on the step's first line.
		steps.Add(container.NewBorder(nil, nil, container.NewVBox(padded(2, 0, 0, 10, number)), nil, parts))
	}
	scroll := container.NewVScroll(steps)
	// As tall as the steps, up to what the window leaves around the dialog.
	room := fyne.Max(300, ui.window.Canvas().Size().Height-260)
	scroll.SetMinSize(fyne.NewSize(0, fyne.Min(steps.MinSize().Height+4, room)))
	back := newRow("Back", nil, styleOutline, func() {
		sheet.dismiss()
		ui.serverSetupMethods(title, connect)
	})
	next := newRow("It runs: connect", nil, stylePrimary, func() {
		sheet.dismiss()
		connect()
	})
	sheet = ui.modal(method.Title, scroll, 640, nil, back, next)
}
