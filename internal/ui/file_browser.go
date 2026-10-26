package ui

// The Files tab: a project's folders and files, as in an editor's explorer,
// with a preview (code coloured, images shown) and actions on each file.
// The same browser shows a local project and a server's (remote desktop).

import (
	"context"
	"fmt"
	"image"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"

	"1loop/internal/remoteapi"
)

// fileSource is where the files are: this computer, or a server.
type fileSource struct {
	name    string // the project, as the header shows it
	list    func(ctx context.Context, folder string) ([]remoteapi.FileEntry, error)
	preview func(ctx context.Context, file string) (remoteapi.FilePreview, error)
	picture func(ctx context.Context, file string) (image.Image, error)
	add     func(ctx context.Context, folder string, paths []string) error // local files copied in
}

// fileAction is a button of a file's preview.
type fileAction struct {
	label string
	icon  fyne.Resource
	run   func(file remoteapi.FileEntry)
}

type fileBrowser struct {
	ctx      context.Context
	window   fyne.Window
	source   fileSource
	actions  []fileAction
	pick     func(done func([]string)) // chooses local files to add
	folder   string
	entries  []remoteapi.FileEntry
	content  *fyne.Container
	status   *widget.Label
	list     *widget.List
	location *canvas.Text
	from, to int // a citation's lines: marked, and in view, in the next file shown
}

func newFileBrowser(ctx context.Context, window fyne.Window, source fileSource, actions []fileAction, pick func(func([]string))) fyne.CanvasObject {
	return buildFileBrowser(ctx, window, source, actions, pick).content
}

func buildFileBrowser(ctx context.Context, window fyne.Window, source fileSource, actions []fileAction, pick func(func([]string))) *fileBrowser {
	browser := &fileBrowser{ctx: ctx, window: window, source: source, actions: actions, pick: pick, content: container.NewStack(), status: widget.NewLabel("")}
	browser.status.Wrapping = fyne.TextWrapWord
	browser.location = textLine(source.name, 13, colText)
	browser.list = widget.NewList(func() int { return len(browser.entries) }, func() fyne.CanvasObject {
		icon := widget.NewIcon(iconDocument)
		name := textLine("", 13, colText)
		size := textLine("", 11, colMuted)
		return container.NewBorder(nil, nil, icon, size, name)
	}, func(i widget.ListItemID, object fyne.CanvasObject) {
		entry := browser.entries[i]
		row := object.(*fyne.Container)
		name, icon, size := row.Objects[0].(*canvas.Text), row.Objects[1].(*widget.Icon), row.Objects[2].(*canvas.Text)
		name.Text = entry.Name
		if entry.Dir {
			icon.SetResource(iconFolder)
			size.Text = ""
		} else {
			icon.SetResource(iconDocument)
			size.Text = byteSize(entry.Size)
		}
		name.Refresh()
		size.Refresh()
	})
	browser.list.OnSelected = func(i widget.ListItemID) {
		browser.list.UnselectAll()
		entry := browser.entries[i]
		if entry.Dir {
			browser.open(entry.Path)
		} else {
			browser.show(entry)
		}
	}
	browser.open("")
	return browser
}

// showLines previews a file of the project with lines from to to marked
// and in view: a citation's.
func (browser *fileBrowser) showLines(file string, from, to int) {
	browser.from, browser.to = from, to
	browser.show(remoteapi.FileEntry{Name: path.Base(file), Path: file})
}

// open lists a folder of the project ("" is its root).
func (browser *fileBrowser) open(folder string) {
	browser.folder = folder
	label := browser.source.name
	if folder != "" {
		label += " / " + strings.ReplaceAll(folder, "/", " / ")
	}
	browser.location.Text = label
	browser.location.Refresh()
	up := newRow("", iconChevronLeft, styleWindow, func() {
		parent := path.Dir(browser.folder)
		if parent == "." {
			parent = ""
		}
		browser.open(parent)
	})
	if folder == "" {
		up.Disable()
	}
	refresh := newRow("Refresh", nil, styleChip, func() { browser.open(browser.folder) })
	var add fyne.CanvasObject = layout.NewSpacer()
	if browser.pick != nil && browser.source.add != nil {
		add = newRow("Add files", iconPlus, styleChip, browser.addFiles)
	}
	header := padded(8, 8, 10, 10, container.NewBorder(nil, nil, up, container.NewHBox(refresh, add), browser.location))
	browser.content.Objects = []fyne.CanvasObject{container.NewBorder(header, padded(4, 6, 12, 12, browser.status), nil, nil, browser.list)}
	browser.content.Refresh()
	browser.status.SetText("Loading…")
	go func() {
		entries, err := browser.source.list(browser.ctx, folder)
		fyne.Do(func() {
			if browser.ctx.Err() != nil || browser.folder != folder {
				return
			}
			if err != nil {
				browser.entries = nil
				browser.status.SetText(err.Error())
			} else {
				browser.entries = entries
				browser.status.SetText(fmt.Sprintf("%d item(s)", len(entries)))
				if len(entries) == 0 {
					browser.status.SetText("This folder is empty.")
				}
			}
			browser.list.Refresh()
		})
	}()
}

func (browser *fileBrowser) addFiles() {
	folder := browser.folder
	browser.pick(func(paths []string) {
		browser.status.SetText(fmt.Sprintf("Adding %d file(s)…", len(paths)))
		go func() {
			err := browser.source.add(browser.ctx, folder, paths)
			fyne.Do(func() {
				if err != nil {
					browser.status.SetText(err.Error())
					return
				}
				if browser.folder == folder {
					browser.open(folder)
				}
			})
		}()
	})
}

// show previews a file, with its actions.
func (browser *fileBrowser) show(file remoteapi.FileEntry) {
	back := newRow("", iconChevronLeft, styleWindow, func() { browser.open(browser.folder) })
	title := textLine(file.Name, 13, colText)
	buttons := container.NewHBox()
	for _, action := range browser.actions {
		action := action
		buttons.Add(newRow(action.label, action.icon, styleChip, func() { action.run(file) }))
	}
	from, to := browser.from, browser.to
	browser.from, browser.to = 0, 0
	body := container.NewStack(padded(20, 20, 18, 18, note("Loading…")))
	header := padded(8, 8, 10, 10, container.NewVBox(container.NewBorder(nil, nil, back, nil, title), container.NewHScroll(buttons)))
	browser.content.Objects = []fyne.CanvasObject{container.NewBorder(header, nil, nil, nil, body)}
	browser.content.Refresh()
	go func() {
		preview, err := browser.source.preview(browser.ctx, file.Path)
		var picture image.Image
		if err == nil && preview.Image && browser.source.picture != nil {
			picture, err = browser.source.picture(browser.ctx, file.Path)
		}
		fyne.Do(func() {
			if browser.ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				body.Objects = []fyne.CanvasObject{padded(20, 20, 18, 18, note(err.Error()))}
			case picture != nil:
				view := canvas.NewImageFromImage(picture)
				view.FillMode = canvas.ImageFillContain
				view.SetMinSize(fyne.NewSize(240, 240))
				body.Objects = []fyne.CanvasObject{padded(12, 12, 12, 12, view)}
			case preview.Binary || preview.Image:
				body.Objects = []fyne.CanvasObject{padded(20, 20, 18, 18, note(fmt.Sprintf("Binary file · %s. Download it to open it.", byteSize(preview.Size))))}
			default:
				text := preview.Text
				if preview.Truncated {
					text += fmt.Sprintf("\n… (the preview shows the first %s of %s)", byteSize(int64(len(preview.Text))), byteSize(preview.Size))
				}
				body.Objects = []fyne.CanvasObject{newCodeViewAt(file.Name, text, from, to)}
			}
			body.Refresh()
		})
	}()
}

// codeLines are a file's lines as the diff view's rows, numbered from
// first and coloured by the file's type.
func codeLines(name string, lines []string, first int) ([]diffLine, float32) {
	rows := make([]diffLine, len(lines))
	width := float32(320)
	for i, line := range lines {
		rows[i] = diffLine{kind: ' ', text: line, next: first + i}
		width = fyne.Max(width, float32(utf8.RuneCountInString(line))*outputCell().Width+110)
	}
	text := strings.Join(lines, "\n") + "\n"
	lexer := lexers.Match(name)
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer != nil {
		if iterator, err := lexer.Tokenise(nil, text); err == nil {
			for i, tokens := range chroma.SplitTokensIntoLines(iterator.Tokens()) {
				if i < len(rows) {
					rows[i].tokens = tokens
				}
			}
		}
	}
	return rows, width
}

// newCodeViewAt shows a file's text with line numbers, coloured by its type,
// with the diff view's rows; lines from to to (1-based, 0: none) are marked
// and scrolled to.
func newCodeViewAt(name, text string, from, to int) fyne.CanvasObject {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > 50000 {
		lines = lines[:50000]
	}
	rows, width := codeLines(name, lines, 1)
	for i := from - 1; from > 0 && i < to && i < len(rows); i++ {
		rows[i].kind = 'c'
	}
	list := widget.NewList(func() int { return len(rows) }, func() fyne.CanvasObject {
		return newDiffRow(width)
	}, func(i widget.ListItemID, object fyne.CanvasObject) {
		row := object.(*diffRow)
		row.line = rows[i]
		row.Refresh()
	})
	list.HideSeparators = true
	if from > 1 && from <= len(rows) {
		// Once the list has its size (before, the offset is clamped to
		// nothing). ScrollTo moves as little as it can: going down it puts
		// the line at the bottom, going up at the top, so it comes from the
		// end. A few lines above stay in view.
		go func() {
			for range 40 {
				time.Sleep(25 * time.Millisecond)
				done := false
				fyne.DoAndWait(func() {
					if done = list.Size().Height > 0; done {
						list.ScrollToBottom()
						list.ScrollTo(max(from-4, 0))
					}
				})
				if done {
					return
				}
			}
		}()
	}
	content := container.NewThemeOverride(list, diffListTheme{fyne.CurrentApp().Settings().Theme()})
	copyText := newRow("Copy", iconCopy, styleChip, func() { fyne.CurrentApp().Clipboard().SetContent(text) })
	header := padded(6, 6, 12, 8, container.NewBorder(nil, nil, textLine(fmt.Sprintf("%d line(s)", len(lines)), 12, colMuted), copyText))
	return container.NewBorder(header, nil, nil, nil, container.NewHScroll(content))
}

func byteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
