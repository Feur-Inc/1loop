// SPDX-License-Identifier: MPL-2.0

package ui

// Settings → Loopy's AutoLinks and Vault tabs.

import (
	"context"
	"fmt"
	"image/color"
	"path"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/loopy"
)

// The tabs of Settings → Loopy.
const (
	loopyTabGeneral = "General"
	loopyTabLinks   = "AutoLinks"
	loopyTabVault   = "Vault"
)

var loopyTabs = []string{loopyTabGeneral, loopyTabLinks, loopyTabVault}

// loopyTabOf is the tab a setting is on.
func loopyTabOf(key string) string {
	switch key {
	case "loopy.autolinks":
		return loopyTabLinks
	case "loopy.vault":
		return loopyTabVault
	}
	return loopyTabGeneral
}

func (ui *desktopUI) loopyTabBar(show func()) fyne.CanvasObject {
	bar := container.NewHBox()
	for _, tab := range loopyTabs {
		chip := newRow(tab, nil, styleChip, func() {
			ui.loopyTab, ui.loopyLink = tab, ""
			show()
		})
		chip.SetSelected(ui.loopyTab == tab)
		bar.Add(chip)
	}
	return padded(8, 2, 0, 0, bar)
}

// linkState is an AutoLink's state in a word, and its colour.
func linkState(link loopy.AutoLink) (string, color.Color) {
	switch {
	case link.Running:
		return "Running", colAmber
	case !link.Allowed:
		return "Not allowed yet", colAmber
	case link.Next != nil:
		return "A change to allow", colAmber
	case link.Waits != "":
		return "Waits for " + link.Waits, colAmber
	case link.Failed > 0:
		return "Failing", colDanger
	case link.When != "" && !link.On:
		return "Off", colMuted
	}
	return "Ready", colAdded
}

// linkSays is what an AutoLink is, in a line.
func linkSays(link loopy.AutoLink) string {
	parts := []string{link.About}
	if link.When != "" {
		parts = append(parts, loopy.Describe(link.When))
	}
	if n := len(link.Runs); n > 0 {
		run := link.Runs[n-1]
		when := run.Time.Local().Format("2 Jan 15:04")
		if run.Error != "" {
			parts = append(parts, "failed "+when)
		} else {
			parts = append(parts, "ran "+when)
		}
	}
	return strings.Join(parts, " · ")
}

// loopyLinkRows is the AutoLinks tab.
func (ui *desktopUI) loopyLinkRows(mode *loopyMode, draw func()) []fyne.CanvasObject {
	for _, link := range mode.view.Links {
		if link.ID == ui.loopyLink {
			return ui.loopyLinkPage(mode, link, draw)
		}
	}
	about := plainFlow("Scripts Loopy makes for itself, to reach a service or do a job in one step instead of driving the browser each time. You allow each one, with what it may reach and use. Ask Loopy for one: \"make an AutoLink that reads my grades\".", 13, colMuted)
	out := []fyne.CanvasObject{padded(10, 4, 0, 0, about)}
	if len(mode.view.Links) == 0 {
		return append(out, card(padded(16, 16, 16, 16, note("No AutoLink yet."))))
	}
	var rows []fyne.CanvasObject
	for _, link := range mode.view.Links {
		state, ink := linkState(link)
		controls := container.NewHBox(container.NewCenter(loopyPill(state, ink)))
		if link.Allowed && link.When != "" {
			controls.Add(newToggle(link.On, func(on bool) {
				do := "link.off"
				if on {
					do = "link.on"
				}
				mode.action(loopy.Action{Do: do, ID: link.ID}, nil)
			}))
		}
		controls.Add(newRow("Open", iconChevronRight, styleChip, func() { ui.loopyLink = link.ID; draw() }))
		rows = append(rows, settingRow(link.Name, linkSays(link), controls))
	}
	return append(out, ui.prefs.mark("loopy.autolinks", card(rows...)))
}

// loopyLinkPage is one AutoLink.
func (ui *desktopUI) loopyLinkPage(mode *loopyMode, link loopy.AutoLink, draw func()) []fyne.CanvasObject {
	id := link.ID
	act := func(do string, on bool) { mode.action(loopy.Action{Do: do, ID: id, On: on}, nil) }
	state, ink := linkState(link)
	out := []fyne.CanvasObject{
		padded(8, 2, 0, 0, container.NewHBox(newRow("All AutoLinks", iconChevronLeft, styleChip, func() { ui.loopyLink = ""; draw() }))),
		padded(4, 2, 0, 0, container.NewHBox(textLine(link.Name, 20, colText), container.NewCenter(loopyPill(state, ink)))),
		plainFlow(link.About, 13, colMuted),
	}
	shown := link
	if link.Next != nil {
		shown = *link.Next
		shown.Allowed = false
	}
	if !shown.Allowed {
		title := "Let it run?"
		if link.Next != nil {
			title = "Allow this change?"
		}
		out = append(out, sectionTitle(title), card(settingRow("Read what it may do below, and its script", "Nothing of it runs before your yes.", container.NewHBox(
			newRow("Run by itself", nil, stylePrimary, func() { act("link.allow", false) }),
			newRow("Ask me each time", nil, styleOutline, func() { act("link.allow", true) })))))
	} else {
		rows := []fyne.CanvasObject{settingRow("Ask me each time", "On: each run waits for your yes. Off: it runs by itself.", newToggle(link.Ask, func(on bool) { act("link.ask", on) }))}
		if link.When != "" {
			rows = append(rows, settingRow("On", loopy.Describe(link.When), newToggle(link.On, func(on bool) {
				if on {
					act("link.on", true)
				} else {
					act("link.off", false)
				}
			})))
		}
		out = append(out, sectionTitle("Running"), card(rows...))
	}
	list := func(title string, values []string, none string) fyne.CanvasObject {
		if len(values) == 0 {
			return settingRow(title, none, nil)
		}
		return settingRow(title, strings.Join(values, ", "), nil)
	}
	var rights []string
	for _, right := range loopy.LinkRights {
		if slices.Contains(shown.Rights, right.Name) {
			rights = append(rights, right.Says)
		}
	}
	sites := shown.Sites
	if slices.Contains(sites, "*") {
		sites = []string{"Any site"}
	}
	may := []fyne.CanvasObject{list("Reaches", sites, "No site"), list("Uses from the vault", shown.Secrets, "Nothing"), list("May", rights, "Nothing more")}
	if len(shown.Libraries) > 0 {
		may = append(may, list("Libraries", shown.Libraries, ""))
	}
	if shown.Input != "" {
		may = append(may, settingRow("Takes", shown.Input, nil))
	}
	if shown.Do != "" {
		may = append(may, settingRow("When it finds something, Loopy does", shown.Do, nil))
	}
	out = append(out, sectionTitle("What it may do"), card(may...))
	buttons := container.NewHBox()
	if link.Allowed {
		if link.Running {
			buttons.Add(newRow("Stop", nil, styleOutline, func() { act("link.stop", false) }))
		} else {
			buttons.Add(newRow("Run now", nil, styleOutline, func() { mode.runLink(link) }))
		}
	}
	buttons.Add(newRow("Script", iconCode, styleOutline, func() { mode.linkScript(link) }))
	buttons.Add(newRow("Edit", iconPencil, styleOutline, func() { mode.editLink(link) }))
	buttons.Add(newRow("Remove", nil, styleOutline, func() {
		ui.confirm("Remove “"+link.Name+"”?", "Its script and what it keeps are deleted. This can't be undone.", "Remove", func() {
			ui.loopyLink = ""
			mode.action(loopy.Action{Do: "link.remove", ID: id}, nil)
		})
	}))
	out = append(out, padded(8, 4, 0, 0, buttons), sectionTitle("History"))
	var history []fyne.CanvasObject
	for i := len(link.Runs) - 1; i >= 0 && len(history) < 15; i-- {
		run := link.Runs[i]
		what := run.Outcome
		if run.Error != "" {
			what = run.Error
		}
		if what == "" {
			what = "Done"
		}
		by := map[string]string{"schedule": "by itself", "you": "by you", "test": "a test"}[run.Why]
		if by == "" {
			by = "by " + run.Why
		}
		var control fyne.CanvasObject
		if len(run.Files) > 0 {
			control = newRow(path.Base(run.Files[0]), nil, styleChip, func() {
				ui.openLoopy()
				mode.openFiles(path.Dir(run.Files[0]))
			})
		}
		history = append(history, settingRow(fmt.Sprintf("%s, %s, %.1f s", run.Time.Local().Format("2 Jan 15:04"), by, float64(run.Took)/1000), what, control))
	}
	if len(history) == 0 {
		history = append(history, padded(14, 14, 16, 16, note("It has not run yet.")))
	}
	return append(out, card(history...))
}

// runLink runs an AutoLink, with its input when it takes one.
func (mode *loopyMode) runLink(link loopy.AutoLink) {
	run := func(text string) { mode.action(loopy.Action{Do: "link.run", ID: link.ID, Text: text}, nil) }
	if link.Input == "" {
		run("")
		return
	}
	input := widget.NewMultiLineEntry()
	input.SetMinRowsVisible(3)
	input.SetPlaceHolder("{ }")
	var dialog *sheet
	dialog = mode.ui.modal("Run “"+link.Name+"”", container.NewVBox(plainFlow("It takes: "+link.Input, 13, colMuted), input), 460, nil,
		newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }),
		newRow("Run", nil, stylePrimary, func() { dialog.dismiss(); run(input.Text) }))
}

// linkScript shows an AutoLink's script, which the user may change.
func (mode *loopyMode) linkScript(link loopy.AutoLink) {
	var code loopy.LinkCode
	mode.do(func(ctx context.Context, client loopy.Client) (err error) {
		code, err = client.Link(ctx, link.ID)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		text := widget.NewMultiLineEntry()
		text.TextStyle = fyne.TextStyle{Monospace: true}
		text.Wrapping = fyne.TextWrapOff
		text.SetMinRowsVisible(20)
		about := "Python. Yours to change: your change is allowed as it is."
		if code.Next != "" {
			text.SetText(code.Next)
			text.Disable()
			about = "The change Loopy asks you to allow."
		} else {
			text.SetText(code.Code)
		}
		problem := plainFlow("", 12.5, colDanger)
		var dialog *sheet
		rows := []*tapRow{newRow("Close", nil, styleOutline, func() { dialog.dismiss() })}
		if code.Next == "" {
			rows = append(rows, newRow("Save", nil, stylePrimary, func() {
				changed := link
				mode.do(func(ctx context.Context, client loopy.Client) error {
					return client.Do(ctx, loopy.Action{Do: "link.edit", ID: link.ID, Link: &changed, Text: text.Text})
				}, func(err error) {
					mode.say("")
					if err != nil {
						problem.spans, problem.width = []span{{text: err.Error()}}, 0
						problem.Refresh()
						return
					}
					dialog.dismiss()
					mode.reload()
				})
			}))
		}
		dialog = mode.ui.modal("Script of “"+link.Name+"”", container.NewVBox(plainFlow(about, 12.5, colMuted), text, problem), 760, nil, rows...)
	})
}

// editLink changes what an AutoLink is and may do.
func (mode *loopyMode) editLink(link loopy.AutoLink) {
	entry := func(text, hint string) *widget.Entry {
		e := widget.NewEntry()
		e.SetText(text)
		e.SetPlaceHolder(hint)
		return e
	}
	join := func(list []string) string { return strings.Join(list, ", ") }
	split := func(text string) (out []string) {
		for _, part := range strings.Split(text, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return
	}
	name, about := entry(link.Name, "Name"), entry(link.About, "What it does")
	sites, secrets := entry(join(link.Sites), "api.example.com, or * for any site"), entry(join(link.Secrets), "Names of vault entries")
	libraries := entry(join(link.Libraries), "requests")
	when := entry(link.When, "every 5m, daily 08:00… or empty")
	do := widget.NewMultiLineEntry()
	do.Wrapping = fyne.TextWrapWord
	do.SetMinRowsVisible(2)
	do.SetText(link.Do)
	body := container.NewVBox()
	add := func(label string, control fyne.CanvasObject) {
		body.Add(container.NewBorder(nil, nil, sized(150, container.NewVBox(padded(8, 0, 0, 0, textLine(label, 13, colMuted)))), nil, control))
	}
	add("Name", name)
	add("What it does", about)
	add("Reaches", sites)
	add("From the vault", secrets)
	add("Libraries", libraries)
	add("Runs", when)
	add("Then Loopy does", do)
	rights := map[string]*widget.Check{}
	for _, right := range loopy.LinkRights {
		check := widget.NewCheck(right.Says, nil)
		check.SetChecked(slices.Contains(link.Rights, right.Name))
		rights[right.Name] = check
		body.Add(check)
	}
	problem := plainFlow("", 12.5, colDanger)
	body.Add(problem)
	var dialog *sheet
	dialog = mode.ui.modal("AutoLink", body, 560, nil, newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }),
		newRow("Save", nil, stylePrimary, func() {
			changed := link
			changed.Name, changed.About, changed.Sites, changed.Secrets, changed.Libraries, changed.When, changed.Do = name.Text, about.Text, split(sites.Text), split(secrets.Text), split(libraries.Text), when.Text, do.Text
			changed.Rights = nil
			for _, right := range loopy.LinkRights {
				if rights[right.Name].Checked {
					changed.Rights = append(changed.Rights, right.Name)
				}
			}
			mode.do(func(ctx context.Context, client loopy.Client) error {
				return client.Do(ctx, loopy.Action{Do: "link.edit", ID: link.ID, Link: &changed})
			}, func(err error) {
				mode.say("")
				if err != nil {
					problem.spans, problem.width = []span{{text: err.Error()}}, 0
					problem.Refresh()
					return
				}
				dialog.dismiss()
				mode.reload()
			})
		}))
}

// ---------------------------------------------------------------------------
// The vault.

// secretForm is a sheet of entries that asks something of Loopy and shows
// what goes wrong in itself.
func (mode *loopyMode) secretForm(title, about, verb string, fields []*widget.Entry, ask func() (loopy.Action, error)) {
	body := container.NewVBox()
	if about != "" {
		body.Add(plainFlow(about, 13, colMuted))
	}
	for _, field := range fields {
		body.Add(field)
	}
	problem := plainFlow("", 12.5, colDanger)
	body.Add(problem)
	say := func(text string) {
		problem.spans, problem.width = []span{{text: text}}, 0
		problem.Refresh()
	}
	var dialog *sheet
	var save *tapRow
	save = newRow(verb, nil, stylePrimary, func() {
		action, err := ask()
		if err != nil {
			say(err.Error())
			return
		}
		save.Disable()
		mode.do(func(ctx context.Context, client loopy.Client) error { return client.Do(ctx, action) }, func(err error) {
			save.Enable()
			mode.say("")
			if err != nil {
				say(err.Error())
				return
			}
			for _, field := range fields {
				field.SetText("")
			}
			dialog.dismiss()
			mode.reload()
		})
	})
	for _, field := range fields {
		if !field.MultiLine {
			field.OnSubmitted = func(string) { save.Tapped(&fyne.PointEvent{}) }
		}
	}
	dialog = mode.ui.modal(title, body, 460, nil, newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }), save)
	mode.ui.window.Canvas().Focus(fields[0])
}

func passwordEntry(hint string) *widget.Entry {
	entry := widget.NewPasswordEntry()
	entry.SetPlaceHolder(hint)
	return entry
}

func (mode *loopyMode) createVault() {
	one, two := passwordEntry("Master password (10 characters or more)"), passwordEntry("Again")
	mode.secretForm("Make your vault", "One password opens it, kept nowhere: if you forget it, what the vault holds is lost. Everything stays where Loopy lives.", "Make it", []*widget.Entry{one, two}, func() (loopy.Action, error) {
		if one.Text != two.Text {
			return loopy.Action{}, fmt.Errorf("The two are not the same.")
		}
		return loopy.Action{Do: "vault.create", Values: map[string]string{"master": one.Text}}, nil
	})
}

func (mode *loopyMode) unlockVault() {
	master := passwordEntry("Master password")
	mode.secretForm("Open your vault", "", "Unlock", []*widget.Entry{master}, func() (loopy.Action, error) {
		return loopy.Action{Do: "vault.unlock", Values: map[string]string{"master": master.Text}}, nil
	})
}

func (mode *loopyMode) changeMaster() {
	old, one, two := passwordEntry("The one you have"), passwordEntry("The new one"), passwordEntry("The new one again")
	mode.secretForm("Change the master password", "", "Change it", []*widget.Entry{old, one, two}, func() (loopy.Action, error) {
		if one.Text != two.Text {
			return loopy.Action{}, fmt.Errorf("The two are not the same.")
		}
		return loopy.Action{Do: "vault.master", Values: map[string]string{"old": old.Text, "master": one.Text}}, nil
	})
}

func (mode *loopyMode) stayOpen() {
	master := passwordEntry("Master password")
	mode.secretForm("Open by itself here", "Where Loopy lives opens the vault without you, so that AutoLinks sign in at night after a restart. Whoever takes over your session there could open it too.", "Allow", []*widget.Entry{master}, func() (loopy.Action, error) {
		return loopy.Action{Do: "vault.stay", On: true, Values: map[string]string{"master": master.Text}}, nil
	})
}

// loopyVaultRows is the Vault tab.
func (ui *desktopUI) loopyVaultRows(mode *loopyMode) []fyne.CanvasObject {
	about := plainFlow("A password manager of your own, where Loopy lives. Loopy knows an entry is there, never its value: 1loop types it in the page, or hands it to an AutoLink you allowed.", 13, colMuted)
	out := []fyne.CanvasObject{padded(10, 4, 0, 0, about)}
	mark := func(rows ...fyne.CanvasObject) fyne.CanvasObject { return ui.prefs.mark("loopy.vault", card(rows...)) }
	switch mode.view.VaultState {
	case "none":
		return append(out, mark(settingRow("No vault yet", "Choose a master password to make one.", newRow("Make it", nil, stylePrimary, mode.createVault))))
	case "locked":
		return append(out, mark(settingRow("Locked", "Its entries show once it is open.", newRow("Unlock", iconShield, stylePrimary, mode.unlockVault))))
	case "":
		return append(out, mark(settingRow("Not available", "The vault could not be read.", nil)))
	}
	out = append(out, mark(settingRow("Open", "", newRow("Lock now", iconShield, styleChip, func() { mode.action(loopy.Action{Do: "vault.lock"}, nil) }))))
	search := widget.NewEntry()
	search.SetPlaceHolder("Search")
	search.SetText(ui.loopyVaultFind)
	list := container.NewStack()
	fill := func() {
		var rows []fyne.CanvasObject
		for _, entry := range mode.view.Vault {
			if !strings.Contains(strings.ToLower(entry.Name+" "+entry.Site+" "+entry.Hint), strings.ToLower(strings.TrimSpace(search.Text))) {
				continue
			}
			detail := map[string]string{"signin": "Sign-in of " + entry.Site, "key": "Key", "contact": "Contact details"}[entry.Kind]
			if slices.Contains(entry.Fields, "totp") {
				detail += " · two-step codes"
			}
			if entry.Hint != "" {
				detail += " · " + entry.Hint
			}
			rows = append(rows, settingRow(entry.Name, detail, container.NewHBox(
				newRow("Show", nil, styleChip, func() { mode.showSecret(entry) }),
				newRow("Edit", nil, styleChip, func() { mode.editSecret(entry) }),
				newRow("Remove", nil, styleChip, func() {
					ui.confirm("Remove "+entry.Name+"?", "The vault forgets it. This can't be undone.", "Remove", func() {
						mode.action(loopy.Action{Do: "vault.remove", ID: entry.ID}, nil)
					})
				}))))
		}
		if len(rows) == 0 {
			text := "Nothing in it yet."
			if strings.TrimSpace(search.Text) != "" {
				text = "Nothing matches."
			}
			rows = append(rows, padded(14, 14, 16, 16, note(text)))
		}
		list.Objects = []fyne.CanvasObject{card(rows...)}
		list.Refresh()
	}
	search.OnChanged = func(text string) { ui.loopyVaultFind = text; fill() }
	fill()
	add := container.NewHBox(
		newRow("Sign-in", iconPlus, styleOutline, func() { mode.editSecret(loopy.VaultEntry{Kind: "signin"}) }),
		newRow("Key", iconPlus, styleOutline, func() { mode.editSecret(loopy.VaultEntry{Kind: "key"}) }),
		newRow("Contact details", iconPlus, styleOutline, func() { mode.editSecret(loopy.VaultEntry{Kind: "contact"}) }))
	out = append(out, padded(6, 6, 0, 0, container.NewBorder(nil, nil, nil, add, search)), list)
	lockName := func(value string) string {
		switch value {
		case "0":
			return "When 1loop closes"
		case "60":
			return "After an hour unused"
		}
		return "After " + value + " minutes unused"
	}
	lock := mode.choice("Lock it", "An AutoLink that needs it then waits for you to open it.", lockName(fmt.Sprint(mode.view.Settings.VaultLock)), []string{"0", "5", "15", "60"}, lockName, func(value string) {
		s := mode.view.Settings
		fmt.Sscan(value, &s.VaultLock)
		mode.set(s)
	})
	stays := newToggle(mode.view.VaultStays, func(on bool) {
		if on {
			mode.stayOpen()
			mode.reload()
			return
		}
		mode.action(loopy.Action{Do: "vault.stay"}, nil)
	})
	return append(out, sectionTitle("Safety"), card(lock,
		settingRow("Open by itself here", "After a restart too, without your master password: for AutoLinks that run while you are away. Less safe.", stays),
		settingRow("Master password", "", newRow("Change", nil, styleChip, mode.changeMaster))))
}

var secretNames = map[string]string{"user": "User name", "password": "Password", "totp": "Two-step secret", "code": "Code now", "key": "Key", "note": "Note", "name": "Name", "email": "E-mail", "phone": "Phone", "address": "Address"}

// showSecret shows an entry's values to the user, for a while.
func (mode *loopyMode) showSecret(entry loopy.VaultEntry) {
	var values map[string]string
	mode.do(func(ctx context.Context, client loopy.Client) (err error) {
		values, err = client.Reveal(ctx, entry.ID)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		body := container.NewVBox()
		var names []string
		for name := range values {
			if name != "totp" {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		for _, name := range names {
			value := values[name]
			label := secretNames[name]
			if label == "" {
				label = name
			}
			hidden := name == "password" || name == "key"
			text := textLine(value, 14, colText)
			text.TextStyle = fyne.TextStyle{Monospace: true}
			if hidden {
				text.Text = strings.Repeat("•", 12)
			}
			buttons := container.NewHBox()
			if hidden {
				buttons.Add(newRow("Show", nil, styleChip, func() { text.Text = value; text.Refresh() }))
			}
			buttons.Add(newRow("Copy", iconCopy, styleChip, func() {
				clipboard := fyne.CurrentApp().Clipboard()
				clipboard.SetContent(value)
				time.AfterFunc(45*time.Second, func() { // not left there
					fyne.Do(func() {
						if clipboard.Content() == value {
							clipboard.SetContent("")
						}
					})
				})
			}))
			body.Add(container.NewBorder(nil, nil, sized(110, container.NewVBox(padded(8, 0, 0, 0, textLine(label, 13, colMuted)))), buttons, container.NewHScroll(container.NewVBox(padded(6, 0, 0, 0, text)))))
		}
		var dialog *sheet
		dialog = mode.ui.modal(entry.Name, body, 520, nil, newRow("Close", nil, styleOutline, func() { dialog.dismiss() }))
		time.AfterFunc(time.Minute, func() { fyne.Do(func() { dialog.dismiss() }) })
	})
}

// editSecret adds or changes an entry of the vault.
func (mode *loopyMode) editSecret(entry loopy.VaultEntry) {
	plain := func(hint, text string) *widget.Entry {
		e := widget.NewEntry()
		e.SetPlaceHolder(hint)
		e.SetText(text)
		return e
	}
	same := ""
	if entry.ID != "" {
		same = " (empty: unchanged)"
	}
	name, hint := plain("Name", entry.Name), plain("When to use it (work account…), optional", entry.Hint)
	site := plain("Site, as example.com", entry.Site)
	values := map[string]*widget.Entry{}
	fields := []*widget.Entry{name}
	title := "A key"
	switch entry.Kind {
	case "signin":
		title = "A sign-in"
		name.SetPlaceHolder("Name (the site's by default)")
		values["user"], values["password"], values["totp"] = plain("User name or address"+same, ""), passwordEntry("Password"+same), passwordEntry("Two-step secret or otpauth:// address, optional")
		fields = []*widget.Entry{site, name, values["user"], values["password"], values["totp"]}
	case "contact":
		title = "Contact details"
		for _, field := range []string{"name", "email", "phone", "address"} {
			values[field] = plain(secretNames[field]+same, "")
			fields = append(fields, values[field])
		}
	default:
		values["key"] = passwordEntry("Key" + same)
		fields = append(fields, values["key"])
	}
	values["note"] = plain("Note, optional", "")
	fields = append(fields, values["note"], hint)
	if values["password"] != nil {
		values["password"].ActionItem = nil
	}
	ask := func() (loopy.Action, error) {
		changed := entry
		changed.Site, changed.Name, changed.Hint = site.Text, name.Text, hint.Text
		if strings.TrimSpace(changed.Name) == "" {
			changed.Name = changed.Site
		}
		out := map[string]string{}
		for field, e := range values {
			out[field] = e.Text
		}
		return loopy.Action{Do: "vault.save", Entry: &changed, Values: out}, nil
	}
	if entry.Kind == "signin" && entry.ID == "" { // a password nobody guesses, at hand
		mode.do(func(ctx context.Context, client loopy.Client) error {
			made, err := client.Reveal(ctx, "")
			if err == nil {
				fyne.Do(func() {
					values["password"].SetPlaceHolder("Password")
					values["password"].ActionItem = newRow("Make one", nil, styleChip, func() {
						values["password"].Password = false
						values["password"].SetText(made["password"])
					})
					values["password"].Refresh()
				})
			}
			return nil
		}, nil)
	}
	mode.secretForm(title, "It stays where Loopy lives, behind your master password. Loopy never sees the values.", "Save", fields, ask)
}
