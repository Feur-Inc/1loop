// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
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

// Add project asks which target the project lives on, and offers every
// linked target: this computer, the SSH machine, the paired server.
// Before #381 it went straight to the target in use (here the machine's
// "New project on build-box"), so a project could not be added on
// another target without switching first.
func TestAddProjectAsksWhichTarget(t *testing.T) {
	ui := addProjectUI(t)
	ui.addProject()
	rows := overlayRows(t, ui)
	for _, want := range []string{"This computer", "build-box", "box.lan"} {
		if !slices.Contains(rows, want) {
			t.Errorf("Add project does not offer %q: %q", want, rows)
		}
	}
	if slices.Contains(rows, "Create") {
		t.Errorf("Add project went straight to the machine in use: %q", rows)
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
