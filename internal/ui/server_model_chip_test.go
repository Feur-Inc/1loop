// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// A server chat's model chip shows the server's effort once, after the
// model's name, and this computer's effort never writes over it (#581).
func TestServerModelChipShowsTheServersEffortOnce(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{}
	ui.model = newPicker(nil, "Model", styleChip, nil)
	ui.model.row.SetTrailing("Low") // this computer's, from before the window connected
	ui.server = &remoteDesktop{parent: ui, models: []remoteapi.Model{{Label: "opus", Name: "Opus 5.5 · Claude Code",
		Efforts: []remoteapi.Effort{{Value: "high", Label: "High"}, {Value: "low", Label: "Low"}}}}}

	ui.server.showModel("opus", "high")
	if ui.model.row.text != "Opus 5.5 · Claude Code" || ui.model.row.trailing != "High" {
		t.Fatalf("chip %q, trailing %q: want the name, then High once", ui.model.row.text, ui.model.row.trailing)
	}

	// The phone, or a control of this computer's, changes its own effort.
	choice := agent.ModelChoice{Efforts: []string{"low", "high"}}
	ui.showModelTrailing(choice, agent.Config{Effort: "low"})
	if ui.model.row.trailing != "High" {
		t.Fatalf("this computer's effort wrote over the server's: %q", ui.model.row.trailing)
	}

	ui.server = nil // back on this computer: its own effort shows again
	ui.showModelTrailing(choice, agent.Config{Effort: "low"})
	if ui.model.row.trailing != agent.EffortLabel("low") {
		t.Fatalf("this computer's chip shows %q", ui.model.row.trailing)
	}
}

// A model the server didn't list keeps its label, with no effort.
func TestServerModelChipUnknownModel(t *testing.T) {
	remote := &remoteDesktop{}
	if text, trailing := remote.modelChip("gone", "high"); text != "gone" || trailing != "" {
		t.Fatalf("%q, %q", text, trailing)
	}
	if text, _ := remote.modelChip("", ""); text != "Model" {
		t.Fatalf("no model: %q", text)
	}
}
