// SPDX-License-Identifier: MPL-2.0

package ui

// Loopy's pages, over its conversation (loopy_view.go): what it remembers,
// its folder, a routine.

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/loopy"
)

// pageHead is a page's way back, its title and what it is.
func (mode *loopyMode) pageHead(title, about string) *fyne.Container {
	page := container.NewVBox(container.NewHBox(newRow("Back to the conversation", iconChevronLeft, styleChip, mode.closePage)),
		padded(6, 6, 0, 0, textLine(title, 24, colText)))
	if about != "" {
		page.Add(plainFlow(about, 13, colMuted))
	}
	return page
}

// ---------------------------------------------------------------------------
// The memory page.

func (mode *loopyMode) openMemory() {
	var memory loopy.MemoryView
	loaded := false
	var draw func()
	read := func() {
		mode.do(func(ctx context.Context, client loopy.Client) (err error) {
			memory, err = client.Memory(ctx)
			return err
		}, func(err error) {
			if err == nil {
				loaded = true
				draw()
			}
		})
	}
	opened := false
	draw = func() {
		page := mode.pageHead("What Loopy remembers", "Every exchange is kept as it was said, and searched when it matters. Facts are drawn from your own words: what you said, and what you pin, is always in its mind; what it only noticed once comes back when it has to do with your message, as a guess. Passwords and card numbers never go here.")
		if !loaded {
			page.Add(spacer(0, 12))
			page.Add(note("Loading…"))
			mode.putPage(page)
			return
		}
		mode.memoryPage(page, memory)
		mode.putPage(page)
	}
	mode.openPage(func() {
		if !opened {
			opened = true
			draw()
		}
		read()
	})
}

func (mode *loopyMode) memoryPage(page *fyne.Container, memory loopy.MemoryView) {
	add := newRow("Add something", iconPlus, styleOutline, func() { mode.editFact(loopy.Fact{Kind: "instruction", Pinned: false}) })
	about := fmt.Sprintf("%d exchanges kept", memory.Records)
	if memory.Waiting > 0 {
		about += fmt.Sprintf(" · %d not read for facts yet", memory.Waiting)
	}
	learn := newRow("Read them now", nil, styleChip, func() { mode.action(loopy.Action{Do: "learn"}, nil) })
	if memory.Waiting == 0 {
		learn.Hide()
	}
	page.Add(padded(10, 4, 0, 0, container.NewBorder(nil, nil, container.NewCenter(textLine(about, 12.5, colFaint)), container.NewHBox(learn, add))))
	if len(memory.Habits) > 0 {
		page.Add(sectionTitle("How you write, as Loopy sees it"))
		var rows []fyne.CanvasObject
		for _, line := range memory.Habits {
			rows = append(rows, padded(10, 10, 16, 16, plainFlow(line, 13, colTextSoft)))
		}
		page.Add(card(rows...))
	}
	current, ended := 0, []loopy.Fact{}
	for _, kind := range loopy.Kinds {
		var rows []fyne.CanvasObject
		for i := len(memory.Facts) - 1; i >= 0; i-- {
			fact := memory.Facts[i]
			if fact.Kind != kind {
				continue
			}
			if !fact.Until.IsZero() {
				ended = append(ended, fact)
				continue
			}
			current++
			rows = append(rows, mode.factRow(fact))
		}
		if len(rows) > 0 {
			page.Add(sectionTitle(loopy.KindNames[kind]))
			page.Add(card(rows...))
		}
	}
	if current == 0 {
		page.Add(spacer(0, 12))
		page.Add(card(padded(16, 16, 16, 16, note("Nothing yet. Tell Loopy \"remember that…\", correct it when it does something wrong, or let it notice as you talk."))))
	}
	if len(ended) > 0 {
		page.Add(sectionTitle("No longer true"))
		var rows []fyne.CanvasObject
		for _, fact := range ended[:min(30, len(ended))] {
			rows = append(rows, settingRow(fact.Text, fact.Since.Local().Format("2 Jan 2006")+" to "+fact.Until.Local().Format("2 Jan 2006"),
				newRow("Forget", nil, styleChip, func() { mode.forgetFact(fact) })))
		}
		page.Add(card(rows...))
	}
	if len(memory.Journal) > 0 {
		page.Add(sectionTitle("Journal"))
		var rows []fyne.CanvasObject
		for i := len(memory.Journal) - 1; i >= 0; i-- {
			rows = append(rows, settingRow(memory.Journal[i].Text, memory.Journal[i].Time.Local().Format("Mon 2 Jan 2006, 15:04"), nil))
		}
		page.Add(card(rows...))
	}
}

func (mode *loopyMode) factRow(fact loopy.Fact) fyne.CanvasObject {
	detail := "Since " + fact.Since.Local().Format("2 Jan 2006")
	switch {
	case fact.Said:
		detail = "You said so · " + detail
	case fact.Times >= 2:
		detail = fmt.Sprintf("Noticed %d times · %s", fact.Times, detail)
	default:
		detail = "Noticed once, only a guess yet · " + detail
	}
	if fact.Pinned {
		detail = "Pinned · " + detail
	}
	if fact.Quote != "" {
		detail += " · “" + fact.Quote + "”"
	}
	pin := "Pin"
	if fact.Pinned {
		pin = "Unpin"
	}
	return settingRow(fact.Text, detail, container.NewHBox(
		newRow(pin, nil, styleChip, func() {
			mode.action(loopy.Action{Do: "fact.change", ID: fact.ID, On: !fact.Pinned}, nil)
		}),
		newRow("Edit", nil, styleChip, func() { mode.editFact(fact) }),
		newRow("Forget", nil, styleChip, func() { mode.forgetFact(fact) })))
}

func (mode *loopyMode) forgetFact(fact loopy.Fact) {
	var dialog *sheet
	also := widget.NewCheck("Also erase the exchange it comes from", nil)
	body := container.NewVBox(plainFlow(fact.Text, 14, colText), spacer(0, 6), also)
	dialog = mode.ui.modal("Forget this?", body, 440, nil,
		newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }),
		newRow("Forget", nil, stylePrimary, func() {
			dialog.dismiss()
			mode.action(loopy.Action{Do: "fact.forget", ID: fact.ID, On: also.Checked}, nil)
		}))
}

// editFact lets the user write or correct a fact and its kind.
func (mode *loopyMode) editFact(fact loopy.Fact) {
	entry := widget.NewMultiLineEntry()
	entry.Wrapping = fyne.TextWrapWord
	entry.SetMinRowsVisible(3)
	entry.SetText(fact.Text)
	entry.SetPlaceHolder("One sentence: “Never book before 10 am”")
	var labels []string
	for _, kind := range loopy.Kinds {
		labels = append(labels, loopy.KindNames[kind])
	}
	kind := widget.NewSelect(labels, nil)
	kind.SetSelected(loopy.KindNames[fact.Kind])
	pinned := widget.NewCheck("Always keep it in mind", nil)
	pinned.SetChecked(fact.Pinned)
	title, do := "Edit what Loopy remembers", "fact.change"
	if fact.ID == "" {
		title, do = "Something to remember", "fact.add"
	}
	var dialog *sheet
	dialog = mode.ui.modal(title, container.NewVBox(entry, container.NewBorder(nil, nil, container.NewCenter(textLine("Kind", 13, colMuted)), nil, kind), pinned), 480, nil,
		newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }),
		newRow("Save", nil, stylePrimary, func() {
			text := strings.TrimSpace(entry.Text)
			if text == "" {
				return
			}
			dialog.dismiss()
			mode.action(loopy.Action{Do: do, ID: fact.ID, Text: text, Kind: loopy.Kinds[max(0, kind.SelectedIndex())], On: pinned.Checked}, nil)
		}))
}

// ---------------------------------------------------------------------------
// Loopy's folder.

func (mode *loopyMode) openFiles(dir string) {
	var files []loopy.File
	loaded := false
	var draw func()
	read := func() {
		mode.do(func(ctx context.Context, client loopy.Client) (err error) {
			files, err = client.Files(ctx, dir)
			return err
		}, func(err error) {
			if err == nil {
				loaded = true
				draw()
			}
		})
	}
	draw = func() {
		page := mode.pageHead("Loopy's folder", "What Loopy keeps for as long as it lives: its notes, what you give it, what its scripts make. It is the only place its scripts see.")
		up := newRow("Up", iconChevronLeft, styleChip, func() { mode.openFiles(path.Dir(dir)) })
		if dir == "/" {
			up.Hide()
		}
		put := newRow("Add files", iconPlus, styleOutline, func() {
			mode.ui.pick(pickRequest{title: "Add to Loopy's folder", multiple: true}, func(paths []string) {
				for _, name := range paths {
					mode.do(func(ctx context.Context, client loopy.Client) error {
						data, err := os.ReadFile(name)
						if err == nil {
							_, err = client.Write(ctx, dir, path.Base(strings.ReplaceAll(name, "\\", "/")), data)
						}
						return err
					}, func(error) { read() })
				}
			})
		})
		page.Add(padded(10, 4, 0, 0, container.NewBorder(nil, nil, container.NewHBox(up, container.NewCenter(textLine(dir, 13, colMuted))), put)))
		var rows []fyne.CanvasObject
		for _, file := range files {
			if file.Dir {
				rows = append(rows, settingRow(path.Base(file.Path)+"/", "Folder", newRow("Open", iconChevronRight, styleChip, func() { mode.openFiles(file.Path) })))
				continue
			}
			rows = append(rows, settingRow(path.Base(file.Path), fmt.Sprintf("%s · %s", fileSizeText(file.Size), file.Time.Local().Format("2 Jan 2006, 15:04")), container.NewHBox(
				newRow("Open", nil, styleChip, func() { mode.openFile(file) }),
				newRow("Save as…", nil, styleChip, func() { mode.saveFile(file) }),
				newRow("Delete", nil, styleChip, func() {
					mode.ui.confirm("Delete "+path.Base(file.Path)+"?", "It is removed from Loopy's folder. This can't be undone.", "Delete", func() {
						mode.action(loopy.Action{Do: "file.remove", Text: file.Path}, func(error) { read() })
					})
				}))))
		}
		switch {
		case !loaded:
			rows = append(rows, padded(16, 16, 16, 16, note("Loading…")))
		case len(rows) == 0:
			rows = append(rows, padded(16, 16, 16, 16, note("Empty.")))
		}
		page.Add(card(rows...))
		mode.putPage(page)
	}
	mode.openPage(draw)
	read()
}

func fileSizeText(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d kB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// openFile shows a text file; another is saved instead.
func (mode *loopyMode) openFile(file loopy.File) {
	var data []byte
	mode.do(func(ctx context.Context, client loopy.Client) (err error) {
		if file.Size > 512<<10 {
			return fmt.Errorf("%s is large: save it to open it", path.Base(file.Path))
		}
		data, err = client.Read(ctx, file.Path)
		return err
	}, func(err error) {
		if err != nil {
			return
		}
		text := string(data)
		if strings.ContainsRune(text, 0) || strings.ToValidUTF8(text, "") != text {
			mode.saveFile(file)
			return
		}
		var content fyne.CanvasObject = loopyText(text, 13, colText)
		if path.Ext(file.Path) == ".md" {
			content = newMarkdownView(text).box
		}
		scroll := newKineticVScroll(content)
		mode.ui.modal(path.Base(file.Path), sizedBox(640, 440, scroll), 680, nil)
	})
}

func sizedBox(width, height float32, object fyne.CanvasObject) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(width, height), object)
}

func (mode *loopyMode) saveFile(file loopy.File) {
	mode.ui.pick(pickRequest{title: "Save a copy", save: true, name: path.Base(file.Path)}, func(paths []string) {
		if len(paths) == 0 {
			return
		}
		mode.do(func(ctx context.Context, client loopy.Client) error {
			data, err := client.Read(ctx, file.Path)
			if err == nil {
				err = os.WriteFile(paths[0], data, 0600)
			}
			return err
		}, nil)
	})
}

// ---------------------------------------------------------------------------
// A routine.

func (mode *loopyMode) openRoutine(id string) {
	mode.openPage(func() {
		var routine loopy.Routine
		for _, r := range mode.view.Routines {
			if r.ID == id {
				routine = r
			}
		}
		if routine.ID == "" {
			mode.closePage()
			return
		}
		page := mode.pageHead(routine.Title, "")
		page.Add(spacer(0, 8))
		when := loopy.Describe(routine.When)
		switch routine.Trigger {
		case "github":
			when = "When there is a new " + routine.Filter + " on GitHub"
		case "mail":
			when = "When a mail comes"
			if routine.Filter != "" {
				when += " about " + routine.Filter
			}
		case "webhook":
			when = "When its address is called"
		}
		rows := []fyne.CanvasObject{
			settingRow("On", "It runs by itself, and asks you before anything that matters.", newToggle(routine.On, func(on bool) {
				do := "routine.off"
				if on {
					do = "routine.on"
				}
				mode.action(loopy.Action{Do: do, ID: id}, nil)
			})),
			settingRow("When", when, nil),
			settingRow("Does", routine.Task, nil),
		}
		if routine.Watch {
			rows = append(rows, settingRow("Watch", "At night too; it speaks only if something breaks.", nil))
		}
		if routine.Trigger == "webhook" {
			rows = append(rows, settingRow("Its address", "POST /api/loopy/hook/"+routine.Token+" on "+mode.view.Host+". Whoever has it can start this routine.", newRow("Copy", iconCopy, styleChip, func() {
				fyne.CurrentApp().Clipboard().SetContent("/api/loopy/hook/" + routine.Token)
			})))
		}
		page.Add(card(rows...))
		page.Add(padded(8, 4, 0, 0, container.NewHBox(
			newRow("Run now", nil, styleOutline, func() { mode.closePage(); mode.action(loopy.Action{Do: "routine.run", ID: id}, nil) }),
			newRow("Edit", iconPencil, styleOutline, func() { mode.editRoutine(routine) }),
			newRow("Remove", nil, styleOutline, func() {
				mode.ui.confirm("Remove this routine?", "Loopy no longer runs it. Its history goes with it.", "Remove", func() {
					mode.closePage()
					mode.action(loopy.Action{Do: "routine.remove", ID: id}, nil)
				})
			}))))
		page.Add(sectionTitle("History"))
		var history []fyne.CanvasObject
		for i := len(routine.Runs) - 1; i >= 0; i-- {
			run := routine.Runs[i]
			outcome := run.Outcome
			if outcome == "" {
				outcome = "running, or cut short"
			}
			history = append(history, settingRow(run.Time.Local().Format("Mon 2 Jan 2006, 15:04"), run.Why+" · "+outcome, nil))
		}
		if len(history) == 0 {
			history = append(history, padded(16, 16, 16, 16, note("It has not run yet.")))
		}
		page.Add(card(history...))
		mode.putPage(page)
	})
}

func (mode *loopyMode) editRoutine(routine loopy.Routine) {
	title, task := widget.NewEntry(), widget.NewMultiLineEntry()
	title.SetText(routine.Title)
	task.Wrapping = fyne.TextWrapWord
	task.SetMinRowsVisible(4)
	task.SetText(routine.Task)
	triggers := []string{"On a schedule", "GitHub", "A mail", "A webhook"}
	values := []string{"", "github", "mail", "webhook"}
	trigger := widget.NewSelect(triggers, nil)
	when := widget.NewEntry()
	when.SetText(routine.When)
	filter := widget.NewEntry()
	filter.SetText(routine.Filter)
	watch := widget.NewCheck("Watch: at night too, speaks only if something breaks", nil)
	watch.SetChecked(routine.Watch)
	sync := func(string) {
		switch values[max(0, trigger.SelectedIndex())] {
		case "":
			when.SetPlaceHolder("daily 08:00 · weekdays 09:30 · mon,thu 18:00 · every 2h")
			when.Show()
			filter.Hide()
		case "github":
			filter.SetPlaceHolder("owner/name issue · pr · ci")
			when.Hide()
			filter.Show()
		case "mail":
			filter.SetPlaceHolder("Words of the sender or subject, or empty")
			when.Hide()
			filter.Show()
		default:
			when.Hide()
			filter.Hide()
		}
	}
	trigger.OnChanged = sync
	for i, value := range values {
		if value == routine.Trigger {
			trigger.SetSelected(triggers[i])
		}
	}
	problem := textLine("", 12.5, colDanger)
	label := func(text string) fyne.CanvasObject { return textLine(text, 12.5, colMuted) }
	var dialog *sheet
	dialog = mode.ui.modal("Edit the routine", container.NewVBox(label("Title"), title, label("What to do each time"), task, label("Starts"), trigger, when, filter, watch, problem), 520, nil,
		newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() }),
		newRow("Save", nil, stylePrimary, func() {
			changed := routine
			changed.Title, changed.Task, changed.When, changed.Trigger, changed.Filter, changed.Watch = title.Text, task.Text, when.Text, values[max(0, trigger.SelectedIndex())], filter.Text, watch.Checked
			mode.do(func(ctx context.Context, client loopy.Client) error {
				return client.Do(ctx, loopy.Action{Do: "routine.edit", Routine: &changed})
			}, func(err error) {
				if err != nil {
					problem.Text = err.Error()
					problem.Refresh()
					mode.say("")
					return
				}
				dialog.dismiss()
				mode.reload()
			})
		}))
}
