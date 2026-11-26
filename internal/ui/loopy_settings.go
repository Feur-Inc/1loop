// SPDX-License-Identifier: MPL-2.0

package ui

// Loopy's settings page, under Agent & approvals: where it lives, its
// model, its summary and night, its memory, its accounts, its vault and
// what the user always allows (loopy_view.go).

import (
	"context"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/loopy"
)

const loopySettingsPage = "Loopy"

func (ui *desktopUI) loopySettings() fyne.CanvasObject {
	view := ui.prefs
	if ui.loopy == nil {
		ui.loopy = newLoopyMode(ui)
	}
	mode := ui.loopy
	page := container.NewVBox()
	draw := func() {
		page.Objects = ui.loopySettingsRows(mode)
		page.Refresh()
	}
	ui.loopySettingsShown = draw
	draw()
	mode.reload()
	return container.NewVBox(view.pageTitle("Loopy", "Agent & approvals", nil),
		note("Loopy is the personal agent you reach from the logo at the top of the sidebar: one conversation apart from your projects, for mail, errands, routines and the coding tasks it hands to an agent in a project."), page)
}

// choice is a row whose control picks one of the values.
func (mode *loopyMode) choice(title, detail, label string, values []string, name func(string) string, set func(string)) fyne.CanvasObject {
	pick := newPicker(mode.ui.window, label, styleField, nil)
	of := map[string]string{}
	for _, value := range values {
		if _, twice := of[name(value)]; !twice {
			of[name(value)] = value
			pick.Options = append(pick.Options, name(value))
		}
	}
	pick.SetSelected(label)
	pick.onChanged = func(chosen string) { set(of[chosen]) }
	return settingRow(title, detail, sized(220, pick.row))
}

func (ui *desktopUI) loopySettingsRows(mode *loopyMode) []fyne.CanvasObject {
	view := ui.prefs
	if view == nil {
		return nil
	}
	if !slices.Contains(loopyTabs, ui.loopyTab) {
		ui.loopyTab = loopyTabGeneral
	}
	bar := ui.loopyTabBar(ui.loopySettingsShown)
	switch ui.loopyTab {
	case loopyTabLinks:
		return append([]fyne.CanvasObject{bar}, ui.loopyLinkRows(mode, ui.loopySettingsShown)...)
	case loopyTabVault:
		return append([]fyne.CanvasObject{bar}, ui.loopyVaultRows(mode)...)
	}
	settings := mode.view.Settings
	change := func(edit func(*loopy.Settings)) {
		s := mode.view.Settings
		edit(&s)
		mode.set(s)
	}

	// Where it lives.
	places := []string{""}
	if ui.home != "" { // the browser client has no Loopy of its own
		places = nil
	}
	for _, address := range loadServerAddresses() {
		if address.Token != "" {
			places = append(places, address.URL)
		}
	}
	placeName := func(at string) string {
		if at == "" {
			return "This computer"
		}
		return strings.TrimPrefix(strings.TrimPrefix(at, "https://"), "http://")
	}
	home := mode.choice("Where Loopy lives", "On this computer, its routines run while 1loop is open. On a server they go on when this computer is off. Each place has a Loopy of its own: its conversation, memory and folder do not move.",
		placeName(ui.loopyServer()), places, placeName, func(at string) { ui.setLoopyAt(at) })

	// The models.
	modelName := func(connection, id, none string) string {
		for _, model := range mode.view.Models {
			if model.ID == id && model.Connection == connection {
				return model.Label
			}
		}
		if id != "" {
			return id
		}
		return none
	}
	modelRow := func(title, detail, none, connection, id string, set func(connection, id string)) fyne.CanvasObject {
		values := []string{none}
		of := map[string]loopy.Model{none: {}}
		for _, model := range mode.view.Models {
			values = append(values, model.Label)
			of[model.Label] = model
		}
		return mode.choice(title, detail, modelName(connection, id, none), values, func(label string) string { return label }, func(label string) {
			set(of[label].Connection, of[label].ID)
		})
	}
	var efforts []string
	for _, model := range mode.view.Models {
		if model.ID == settings.Model && model.Connection == settings.Connection {
			efforts = model.Efforts
		}
	}
	effortName := func(value string) string {
		if value == "" {
			return "The model's default"
		}
		return strings.ToUpper(value[:1]) + value[1:]
	}
	models := []fyne.CanvasObject{view.mark("loopy.model", modelRow("Loopy's model", "Its own model, from the connections of where it lives, or the one chosen for your projects.", "As in 1loop", settings.Connection, settings.Model, func(connection, id string) {
		change(func(s *loopy.Settings) { s.Connection, s.Model, s.Effort = connection, id, "" })
	}))}
	if len(efforts) > 0 {
		models = append(models, mode.choice("Reasoning effort", "", effortName(settings.Effort), append([]string{""}, efforts...), effortName, func(value string) {
			change(func(s *loopy.Settings) { s.Effort = value })
		}))
	}
	models = append(models, modelRow("Memory's model", "It reads what you wrote, from time to time, for facts worth keeping. A small model does.", "The same as Loopy's", settings.LearnerConnection, settings.Learner, func(connection, id string) {
		change(func(s *loopy.Settings) { s.LearnerConnection, s.Learner = connection, id })
	}))

	// Every day.
	summaryName := func(value string) string {
		switch value {
		case "", "08:00":
			return "At 08:00"
		case "off":
			return "Never"
		}
		return "At " + value
	}
	nightName := func(value string) string {
		if value == "-" {
			return "No night"
		}
		from, to, _ := strings.Cut(value, "-")
		return from + " to " + to
	}
	night := "23:00-07:00"
	if settings.QuietFrom != "" || settings.QuietTo != "" {
		night = settings.QuietFrom + "-" + settings.QuietTo
		if settings.QuietFrom == settings.QuietTo {
			night = "-"
		}
	}
	wants := widget.NewMultiLineEntry()
	wants.Wrapping = fyne.TextWrapWord
	wants.SetMinRowsVisible(2)
	wants.SetText(settings.Wants)
	wants.SetPlaceHolder("Only what needs me today, and CI on dev…")
	saveWants := newRow("Save", nil, styleChip, func() { change(func(s *loopy.Settings) { s.Wants = strings.TrimSpace(wants.Text) }) })
	day := []fyne.CanvasObject{
		view.mark("loopy.summary", mode.choice("Morning summary", "What needs you and what is in progress, in a card, once a day.", summaryName(settings.Summary), []string{"06:30", "07:00", "07:30", "08:00", "08:30", "09:00", "10:00", "off"}, summaryName, func(value string) {
			change(func(s *loopy.Settings) { s.Summary = value })
		})),
		mode.choice("Night", "Only a watch that breaks, or a card you must answer, notifies you then.", nightName(night), []string{"22:00-07:00", "23:00-07:00", "23:00-08:00", "00:00-09:00", "-"}, nightName, func(value string) {
			from, to, _ := strings.Cut(value, "-")
			if value == "-" {
				from, to = "00:00", "00:00"
			}
			change(func(s *loopy.Settings) { s.QuietFrom, s.QuietTo = from, to })
		}),
		padded(12, 12, 16, 16, container.NewVBox(textLine("What you want to hear about", 14, colText), wants, container.NewHBox(saveWants))),
	}

	// Memory.
	memory := []fyne.CanvasObject{
		view.mark("loopy.memory", settingRow("What Loopy remembers", "Rules, how you like answers, facts about you, and its journal. Edit, pin or forget any of them.", newRow("Open", iconChevronRight, styleChip, func() {
			ui.openLoopy()
			mode.openMemory()
		}))),
		settingRow("Learn on its own", "On: Loopy also keeps what your words show of your ways (a rule, a style, a habit, a person). Off: only what you ask it to remember.",
			newToggle(!settings.AskToRemember, func(on bool) { change(func(s *loopy.Settings) { s.AskToRemember = !on }) })),
		settingRow("Journal", "Loopy notes what it did, dated, and searches it later.",
			newToggle(!settings.JournalOff, func(on bool) { change(func(s *loopy.Settings) { s.JournalOff = !on }) })),
		settingRow("Its folder", "Its notes, and what you give it.", newRow("Open", iconChevronRight, styleChip, func() {
			ui.openLoopy()
			mode.openFiles("/")
		})),
	}

	// Accounts.
	var accounts []fyne.CanvasObject
	for _, account := range mode.view.Accounts {
		what := "Mail: Loopy reads it. What it writes is sent only from a card you press."
		if account.Kind == "calendar" {
			what = "Calendar: Loopy reads it. What it adds is added only from a card you press."
		}
		accounts = append(accounts, settingRow(account.Address, what, newRow("Remove", nil, styleChip, func() {
			ui.confirm("Remove "+account.Address+"?", "Loopy no longer reaches this account. Its password is forgotten.", "Remove", func() {
				mode.action(loopy.Action{Do: "account.remove", ID: account.ID}, nil)
			})
		})))
	}
	accounts = append(accounts, settingRow("Connect an account", "Mail (IMAP and SMTP) or a calendar (CalDAV), with an app password.", container.NewHBox(
		newRow("Mail", iconPlus, styleChip, func() { mode.addAccount("mail") }), newRow("Calendar", iconPlus, styleChip, func() { mode.addAccount("calendar") }))))

	out := []fyne.CanvasObject{
		bar,
		sectionTitle("Home"), card(view.mark("loopy.home", home)),
		sectionTitle("Model"), card(models...),
		sectionTitle("Every day"), card(day...),
		sectionTitle("Memory"), card(memory...),
		sectionTitle("Accounts"), card(view.mark("loopy.accounts", container.NewVBox(accounts...))),
	}
	if len(mode.view.Rules) > 0 {
		var rules []fyne.CanvasObject
		for _, rule := range mode.view.Rules {
			what := "Sign in to " + rule.Site + " with the vault"
			if rule.Kind == "script" {
				what = "Scripts that act on " + rule.Site
			}
			if kind, ok := strings.CutPrefix(rule.Kind, "browser:"); ok {
				what = strings.ToUpper(kind[:1]) + kind[1:] + " on " + rule.Site
			}
			rules = append(rules, settingRow(what, "Allowed without asking since "+rule.Time.Local().Format("2 Jan 2006"), newRow("Ask again", nil, styleChip, func() {
				mode.action(loopy.Action{Do: "rule.remove", ID: rule.ID}, nil)
			})))
		}
		out = append(out, sectionTitle("Always allowed"), card(rules...))
	}
	return out
}

// addAccount connects a mail or calendar account.
func (mode *loopyMode) addAccount(kind string) {
	address, password := widget.NewEntry(), widget.NewPasswordEntry()
	address.SetPlaceHolder("you@example.com")
	password.SetPlaceHolder("App password")
	imap, smtp, caldav := widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	imap.SetPlaceHolder("IMAP server (known for the common providers)")
	smtp.SetPlaceHolder("SMTP server (known for the common providers)")
	caldav.SetPlaceHolder("https://… (known for the common providers)")
	fields := []fyne.CanvasObject{address, password}
	title := "Connect a mail account"
	if kind == "mail" {
		fields = append(fields, imap, smtp)
	} else {
		title = "Connect a calendar"
		fields = append(fields, caldav)
	}
	problem := plainFlow("", 12.5, colDanger)
	fields = append(fields, plainFlow("Most providers want a password made for apps, in their security settings. It is kept encrypted in Loopy's vault.", 12.5, colMuted), problem)
	var dialog *sheet
	var connect *tapRow
	connect = newRow("Connect", nil, stylePrimary, func() {
		account := loopy.Account{Kind: kind, Address: address.Text, Password: password.Text, IMAP: imap.Text, SMTP: smtp.Text, CalDAV: caldav.Text}
		connect.Disable()
		mode.do(func(ctx context.Context, client loopy.Client) error {
			return client.Do(ctx, loopy.Action{Do: "account.save", Account: &account})
		}, func(err error) {
			connect.Enable()
			mode.say("")
			if err != nil {
				problem.spans = []span{{text: err.Error()}}
				problem.width = 0
				problem.Refresh()
				return
			}
			dialog.dismiss()
			mode.reload()
		})
	})
	dialog = mode.ui.modal(title, container.NewVBox(fields...), 480, nil, newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }), connect)
}
