// SPDX-License-Identifier: MPL-2.0

package ui

// Loopy's settings page, under Agent & approvals: its model, its morning
// summary, its context and its memory (loopy_view.go).

import (
	"path/filepath"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"1loop/internal/loopy"
)

const loopySettingsPage = "Loopy"

// loopyStore is Loopy's state, read from its file when Loopy was never
// opened in this session.
func (ui *desktopUI) loopyStore() *loopy.Store {
	if ui.loopy != nil {
		return ui.loopy.store
	}
	return loopy.Open(filepath.Join(filepath.Dir(ui.statePath), "loopy.json"))
}

func (ui *desktopUI) loopySettings() fyne.CanvasObject {
	view := ui.prefs
	store := ui.loopyStore()
	save := func() {
		go store.Save()
		if ui.loopy != nil {
			ui.loopy.modelChip.SetText(ui.loopy.modelLabel())
		}
	}
	settings := store.Settings

	// The model: Loopy's own, or 1loop's.
	modelLabel := "As in 1loop"
	if settings.Model != "" {
		modelLabel = settings.Model
	}
	var model *tapRow
	model = newRow(modelLabel, iconChevron, styleChip, func() {
		labels := make([]string, 0, len(ui.choices))
		for label := range ui.choices {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		set := func(connection, id, label string) {
			store.Lock()
			store.Settings.Connection, store.Settings.Model = connection, id
			store.Unlock()
			model.SetText(label)
			save()
		}
		actions := []menuAction{{label: "As in 1loop", detail: "The model chosen in your projects", checked: store.Settings.Model == "", run: func() { set("", "", "As in 1loop") }}}
		for _, label := range labels {
			choice := ui.choices[label]
			if choice.Connection.Enabled {
				actions = append(actions, menuAction{label: label, checked: choice.ID == store.Settings.Model && choice.Connection.ID == store.Settings.Connection,
					run: func() { set(choice.Connection.ID, choice.ID, choice.ID) }})
			}
		}
		showMenu(ui.window, model, actions)
	})
	efforts := []string{"", "low", "medium", "high"}
	effortNames := map[string]string{"": "The model's default", "low": "Low", "medium": "Medium", "high": "High"}
	var effort *tapRow
	effort = newRow(effortNames[settings.Effort], iconChevron, styleChip, func() {
		var actions []menuAction
		for _, value := range efforts {
			actions = append(actions, menuAction{label: effortNames[value], checked: store.Settings.Effort == value, run: func() {
				store.Lock()
				store.Settings.Effort = value
				store.Unlock()
				effort.SetText(effortNames[value])
				save()
			}})
		}
		showMenu(ui.window, effort, actions)
	})

	// The morning summary.
	times := []string{"07:00", "07:30", "08:00", "08:30", "09:00", "off"}
	summaryLabel := func(value string) string {
		switch value {
		case "", "08:00":
			return "At 08:00"
		case "off":
			return "Never"
		}
		return "At " + value
	}
	var summary *tapRow
	summary = newRow(summaryLabel(settings.Summary), iconChevron, styleChip, func() {
		var actions []menuAction
		for _, value := range times {
			actions = append(actions, menuAction{label: summaryLabel(value), checked: summaryLabel(store.Settings.Summary) == summaryLabel(value), run: func() {
				store.Lock()
				store.Settings.Summary = value
				store.Unlock()
				summary.SetText(summaryLabel(value))
				save()
			}})
		}
		showMenu(ui.window, summary, actions)
	})

	// Memory.
	setFlag := func(set func(*loopy.Settings, bool)) func(bool) {
		return func(on bool) {
			store.Lock()
			set(&store.Settings, on)
			store.Unlock()
			save()
		}
	}
	openMemory := newRow("Open", iconChevronRight, styleChip, func() {
		ui.openLoopy()
		ui.loopy.openMemory()
	})

	return container.NewVBox(
		view.pageTitle("Loopy", "Agent & approvals", nil),
		note("Loopy is the personal agent you reach from the logo at the top of the sidebar: one conversation apart from your projects, for mail, errands, routines and the coding tasks it hands to an agent in a project."),
		sectionTitle("Model"),
		card(view.mark("loopy.model", settingRow("Loopy's model", "Its own model, from your connections, or the one chosen in your projects.", model)),
			settingRow("Reasoning effort", "For a model with effort levels.", effort)),
		sectionTitle("Every day"),
		card(view.mark("loopy.summary", settingRow("Morning summary", "What is in progress and what needs you, in a card, once a day.", summary))),
		sectionTitle("Context"),
		card(settingRow("Compaction", "Loopy's long conversation is compacted by levels at its natural breaks, as your project chats are, when Dynamic compaction is on; else once when it is nearly full. The whole conversation stays in Loopy's memory: it searches it when it needs an older part.",
			newToggle(!ui.classicCompaction, func(on bool) { ui.setDynamicCompaction(on) }))),
		sectionTitle("Memory"),
		card(view.mark("loopy.memory", settingRow("What Loopy remembers", "Facts about you by kind, pinned or not, and its journal. Edit, pin or forget any of them.", openMemory)),
			settingRow("Remember on its own", "On: Loopy also keeps what clearly matters (a preference, a person, a place) and tells you. Off: only what you ask it to remember.",
				newToggle(!settings.AskToRemember, setFlag(func(s *loopy.Settings, on bool) { s.AskToRemember = !on }))),
			settingRow("Journal", "Loopy notes what it did, dated, and searches it later.",
				newToggle(!settings.JournalOff, setFlag(func(s *loopy.Settings, on bool) { s.JournalOff = !on })))),
	)
}
