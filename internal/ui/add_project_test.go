// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// overlayRows are the texts of the rows of the topmost sheet.
func overlayRows(t *testing.T, ui *desktopUI) (rows []string) {
	t.Helper()
	top := ui.window.Canvas().Overlays().Top()
	dialog, ok := top.(*sheet)
	if !ok {
		t.Fatalf("no sheet is open (top overlay %T)", top)
	}
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch object := object.(type) {
		case *tapRow:
			rows = append(rows, object.text)
		case *fyne.Container:
			for _, child := range object.Objects {
				walk(child)
			}
		}
	}
	walk(dialog.content)
	return rows
}

// addProjectUI is sidebarUI in a window, an SSH machine in use and a
// server paired: three targets a project can live on.
func addProjectUI(t *testing.T) *desktopUI {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ui := linkedUI(t)
	ui.window = test.NewWindow(widget.NewLabel(""))
	ui.window.Resize(fyne.NewSize(900, 700))
	t.Cleanup(ui.window.Close)
	ui.machine = newRow("", nil, styleNav, nil)
	ui.cfg.MachineID, ui.cfg.Workspace = "m1", "/srv/site"
	return ui
}

// Add project is one New project dialog (#408): where the project lives,
// set to the target in use and offering every linked one, its name, its
// folders, its environment variables, then Cancel and Create. Before it
// was a list of targets (#381), then a second dialog for the folder.
func TestAddProjectIsOneDialog(t *testing.T) {
	ui := addProjectUI(t)
	ui.addProject()
	top, ok := ui.window.Canvas().Overlays().Top().(*sheet)
	if !ok {
		t.Fatal("Add project opened no dialog")
	}
	texts := visibleTexts(top.content)
	for _, want := range []string{"New project", "Location", "build-box", "Name", "Environment variables", "None", "Edit", "Cancel", "Create"} {
		if !slices.Contains(texts, want) {
			t.Errorf("the dialog lacks %q: %q", want, texts)
		}
	}
	if slices.Contains(texts, "Folders (optional)") {
		t.Error("an SSH machine's project lists folders of this computer")
	}
	if entries := visibleEntries(top.content); len(entries) != 1 || entries[0].PlaceHolder != "Project name" {
		t.Errorf("the dialog's fields: %d", len(entries))
	}
}

// Only linked targets are offered: no server when none is paired, and
// nothing to ask when this computer is the only target.
func TestAddProjectOffersOnlyLinkedTargets(t *testing.T) {
	ui := addProjectUI(t)
	want := []projectTarget{localTarget, sshTarget("m1"), serverTarget(testServer)}
	if got := ui.addTargets(); !slices.Equal(got, want) {
		t.Fatalf("targets %v, want %v", got, want)
	}
	ui.links = nil
	if got := ui.addTargets(); !slices.Equal(got, want[:2]) {
		t.Fatalf("with no server paired: %v, want %v", got, want[:2])
	}
	ui.sshMachines = nil
	if got := ui.addTargets(); !slices.Equal(got, []projectTarget{localTarget}) {
		t.Fatalf("with this computer only: %v", got)
	}
}

// A project added on a chosen target is kept under that target, whatever
// the target in use: this computer's folder is never listed as the SSH
// machine's, nor the machine's as this computer's.
func TestProjectAddedIsKeptUnderItsTarget(t *testing.T) {
	ui := addProjectUI(t) // the SSH machine is in use
	ui.keepProjectOn(localTarget, "/home/u/new")
	ui.keepProjectOn(sshTarget("m1"), "/srv/new")
	if !slices.Equal(ui.projects, []string{"/home/u/app", "/home/u/new"}) {
		t.Errorf("this computer's projects %q", ui.projects)
	}
	if !slices.Equal(ui.machineProjects["m1"], []string{"/srv/site", "/srv/new"}) {
		t.Errorf("the machine's projects %q", ui.machineProjects["m1"])
	}
	ui.keepProjectOn(serverTarget(testServer), "/srv/api2") // the server keeps its own list
	if slices.Contains(ui.projects, "/srv/api2") || slices.Contains(ui.machineProjects["m1"], "/srv/api2") {
		t.Error("a server's project was kept as another target's")
	}
	ui.renderChats()
	wantListed(t, ui, "new", "app", "site")
	for _, group := range ui.sidebarGroups() {
		switch group.target {
		case localTarget:
			if !slices.Contains(group.projects, "/home/u/new") {
				t.Errorf("this computer's group lacks the new project: %q", group.projects)
			}
		case sshTarget("m1"):
			if !slices.Contains(group.projects, "/srv/new") {
				t.Errorf("the machine's group lacks the new project: %q", group.projects)
			}
		}
	}
}

// visibleTexts are the texts shown under object: its rows, lines and
// paragraphs, hidden parts left out.
func visibleTexts(object fyne.CanvasObject) (texts []string) {
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		if object == nil || !object.Visible() {
			return
		}
		switch object := object.(type) {
		case *tapRow:
			texts = append(texts, object.text)
		case *canvas.Text:
			texts = append(texts, object.Text)
		case *flowText:
			var text strings.Builder
			for _, span := range object.spans {
				text.WriteString(span.text)
			}
			texts = append(texts, text.String())
		case *widget.Check:
			texts = append(texts, object.Text)
		case *fyne.Container:
			for _, child := range object.Objects {
				walk(child)
			}
		case *container.Scroll:
			walk(object.Content)
		}
	}
	walk(object)
	return texts
}

// visibleEntries are the text fields shown under object.
func visibleEntries(object fyne.CanvasObject) (entries []*widget.Entry) {
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		if object == nil || !object.Visible() {
			return
		}
		switch object := object.(type) {
		case *widget.Entry:
			entries = append(entries, object)
		case *fyne.Container:
			for _, child := range object.Objects {
				walk(child)
			}
		case *container.Scroll:
			walk(object.Content)
		}
	}
	walk(object)
	return entries
}

// tapText taps the visible row of that text under object.
func tapText(t *testing.T, object fyne.CanvasObject, text string) {
	t.Helper()
	var found *tapRow
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		if object == nil || !object.Visible() || found != nil {
			return
		}
		switch object := object.(type) {
		case *tapRow:
			if object.text == text {
				found = object
			}
		case *fyne.Container:
			for _, child := range object.Objects {
				walk(child)
			}
		case *container.Scroll:
			walk(object.Content)
		}
	}
	walk(object)
	if found == nil {
		t.Fatalf("no %q to tap", text)
	}
	found.onTap()
}
