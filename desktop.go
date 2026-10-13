package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type conversation struct {
	ID       string        `json:"id,omitempty"` // stable, for the phone remote
	Title    string        `json:"title"`
	Config   config        `json:"config"`
	History  []message     `json:"history"`
	Items    []chatItem    `json:"items"`
	Cache    *contextCache `json:"contextCache,omitempty"`
	Todo     *todoState    `json:"todo,omitempty"`
	Panel    *savedPanel   `json:"panel,omitempty"` // its side panel, to open it again as it was
	Version  string        `json:"version,omitempty"`
	Versions []chatVersion `json:"versions,omitempty"`

	panel     *panelState // its side panel while 1loop runs (live tabs, terminals included)
	todoCount int         // tasks last seen in its list: the first ones open the panel

	run   *chatRun  // the agent loop working on this chat, if any
	queue []message // Enter while it runs: the next turns
	steer []message // Ctrl+Enter while it runs: joins at the next step
}

// chatRun is an agent loop at work. Each chat has its own, so the user can
// open other chats, settings or projects while it runs in the background.
type chatRun struct {
	cancel   context.CancelFunc
	turn     *turn
	approval *sheet
	// The pending approval, so the phone remote can show and answer it.
	approvalID, approvalTitle, approvalPreview string
	answer                                     func(allow bool)
	started                                    time.Time
}

type desktopUI struct {
	window           fyne.Window
	backend          *app
	cfg              config
	keys             map[string]string
	connectionsList  []connection
	choices          map[string]modelChoice
	modelUnavailable map[string]string // model catalog problems, displayed inside the selector
	accountLimits    map[string]accountLimits
	userInstructions string
	approvalMode     string
	chats            []*conversation
	current          *conversation
	projects         []string
	statePath        string
	saveTimer        *time.Timer
	saveLock         sync.Mutex // one write at a time, the newest state last
	saveCount        int        // states handed to persist
	saveDone         int        // the newest one written (under saveLock)
	saveError        string
	ctx              context.Context
	shutdown         context.CancelFunc
	modelLoad        int
	plan             planUsage      // Claude Code connection: the subscription windows in use
	claudeOffered    bool           // the Claude Code connection was proposed once
	remote           *remoteServer  // the phone remote, nil when off
	remoteEnabled    bool           // see remote.go
	devices          []pairedDevice // phones paired with the remote
	modelsLoading    bool
	chrome           bool

	// Composer
	prompt          *chatEntry
	attach          *tapRow
	attachRow       *fyne.Container
	attachTray      fyne.CanvasObject // attachRow with its margins, hidden when empty
	attachments     []ImagePart
	editing         *queueEdit // a queued message being edited in the prompt
	editBar         *fyne.Container
	queueTray       *fyne.Container
	queueList       *fyne.Container
	queueTitle      *fitLabel
	model           *picker
	approvalControl *tapRow
	contextRing     *contextRing
	send            *tapRow
	stop            *tapRow
	controls        *fyne.Container
	folder          *tapRow

	// Conversation
	transcript        *fyne.Container
	column            *fyne.Container
	scroll            *kineticScroll
	followTail        bool
	items             *transcriptLayout
	frames            *frameHook
	prefs             *settingsView     // the settings page
	sideApp           fyne.CanvasObject // the sidebar's usual content, hidden while settings are open
	selected          textSelection     // what holds the selected text, if anything does
	building          bool              // openChat is adding a whole chat's items (see place)
	welcome           fyne.CanvasObject
	thinking          *fyne.Container
	thinkLabel        *fitLabel
	inlineRow         fyne.CanvasObject // the activity line's place under the last message
	inlineSlot        *fyne.Container
	dockRow           *fyne.Container // its place beside the composer (see dockActivity)
	composer          fyne.CanvasObject
	docked            bool
	lineShown         bool
	mark              *loopMark
	workMark          *loopMark
	views             map[int]any             // live widgets of the chat on screen, by item index
	copyRows          map[int]*fyne.Container // Copy buttons under its answers, by item index
	settled           *time.Timer
	panel             *toolPanel
	split             *fyne.Container
	panelToggle       *tapRow
	panelWidth        float32 // last width chosen by dragging, independent of window size
	classicCompaction bool    // settings: dynamic context compaction turned off
	tips              *tipLayer

	// Window and sidebar
	header      *fitLabel
	headerBar   *fyne.Container
	expand      *tapRow
	newChat     *tapRow
	connections *tapRow
	projectList *fyne.Container
	chatList    *fyne.Container
	sideLists   *fyne.Container
	pane        *paneLayout
	paneAnim    *fyne.Animation
	root        *fyne.Container
}

// Fyne snapshots its built-in dialog language when loading translations.
// Restore LANGUAGE immediately so shell tools keep the user's environment.
func useEnglishUI() {
	previous, present := os.LookupEnv("LANGUAGE")
	os.Setenv("LANGUAGE", "en")
	_ = lang.AddTranslationsForLocale([]byte("{}"), fyne.Locale("en"))
	if present {
		os.Setenv("LANGUAGE", previous)
	} else {
		os.Unsetenv("LANGUAGE")
	}
}

func runDesktop(backend *app, root string) {
	useEnglishUI()
	switchBackend()
	if devWanted {
		startDevLog()
	}
	go installLauncher()
	// Follow the desktop's own scale (Xft.dpi) instead of Fyne's guess from
	// the panel's physical DPI, which made everything ~40 % larger than other
	// apps. An explicit FYNE_SCALE from the user still wins.
	if os.Getenv("FYNE_SCALE") == "" {
		if scale := systemScale(); scale > 0 {
			os.Setenv("FYNE_SCALE", fmt.Sprintf("%.2f", scale))
			os.Setenv("FYNE_DISABLE_DPI_DETECTION", "1")
		}
	}
	a := fyneapp.NewWithID("io.1loop.desktop")
	a.Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ctx, cancel := context.WithCancel(context.Background())
	u := &desktopUI{backend: backend, ctx: ctx, shutdown: cancel, keys: make(map[string]string), statePath: statePath(root),
		cfg: config{Provider: "vllm", BaseURL: defaultVLLMURL, Workspace: root}}
	u.load()
	u.initializeConnections()
	// Our own title bar on X11 and native Wayland; the system bar elsewhere.
	u.chrome = customChromeSupported() && os.Getenv("ONELOOP_SYSTEM_TITLEBAR") == ""
	if drv, ok := a.Driver().(desktop.Driver); ok && u.chrome {
		u.window = drv.CreateSplashWindow()
		u.window.SetTitle("1loop")
		u.window.SetMaster()
	} else {
		u.chrome = false
		u.window = a.NewWindow("1loop")
	}
	u.window.SetPadded(false)
	u.window.Resize(fyne.NewSize(1180, 760))
	u.window.CenterOnScreen()
	u.window.SetIcon(brandResource("1loop-anneau-fond-noir.png"))
	u.window.SetContent(u.build())
	u.window.SetCloseIntercept(u.quit)
	for _, s := range []*desktop.CustomShortcut{
		{KeyName: fyne.KeyK, Modifier: fyne.KeyModifierControl},
		{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift},
		{KeyName: fyne.KeyB, Modifier: fyne.KeyModifierControl},
	} {
		u.window.Canvas().AddShortcut(s, func(s fyne.Shortcut) { u.shortcut(s) })
	}
	u.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { u.copySelection() })
	u.updateHeader()
	u.renderChats()
	u.refreshModels()
	u.refreshContextRing()
	u.startRemote()
	u.window.Canvas().Focus(u.prompt)
	// The launch sequence covers the first frames and only starts once the
	// window is really on screen, otherwise it would play unseen.
	launch := newLaunchScreen(u.window.Canvas(), func() { u.window.Canvas().Focus(u.prompt) })
	launch.show()
	a.Lifecycle().SetOnStarted(func() {
		if u.chrome {
			chromeReady()
		}
		windowIdentity(u.window) // again, should the window not have existed yet below
		u.startFrames()
		size := u.window.Canvas().Size()
		devf("app", "window %.0f x %.0f at scale %.2f, own title bar %v", size.Width, size.Height, u.window.Canvas().Scale(), u.chrome)
		time.AfterFunc(180*time.Millisecond, func() { fyne.Do(launch.play) })
	})
	// Shown, named, then run: the desktop learns which application this is
	// before the window's first frame, when it picks its name and icon.
	u.window.Show()
	windowIdentity(u.window)
	a.Run()
}

// statePath keeps conversations and settings in the user's config folder
// (~/.config/1loop), never inside the project. A state file left by earlier
// versions in <workspace>/.1loop is moved there once.
func statePath(root string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(root, ".1loop", "state.json")
	}
	path := filepath.Join(dir, "1loop", "state.json")
	legacy := filepath.Join(root, ".1loop", "state.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if data, err := os.ReadFile(legacy); err == nil && os.MkdirAll(filepath.Dir(path), 0700) == nil {
			if os.WriteFile(path, data, 0600) == nil {
				os.Remove(legacy)
				os.Remove(filepath.Dir(legacy))
			}
		}
	}
	return path
}

func (u *desktopUI) quit() {
	u.release()
	u.window.SetCloseIntercept(nil)
	u.window.Close()
}

// restart (Ctrl+Shift+R) reloads everything: state is saved, the running
// request is stopped, then the process replaces itself with a fresh copy,
// so the interface, the engine and the launch animation all start again.
func (u *desktopUI) restart() {
	exe, err := os.Executable()
	if err != nil {
		u.notice("Restart", err.Error())
		return
	}
	devf("ui", "restart (Ctrl+Shift+R)")
	u.release()
	// The binary may be in the middle of being rebuilt: wait for it rather
	// than fail, since after release this process cannot go on.
	var failure error
	for attempt := 0; attempt < 50; attempt++ {
		failure = syscall.Exec(exe, os.Args, os.Environ())
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "1loop: could not restart:", failure)
	os.Exit(1)
}

func (u *desktopUI) shortcut(s fyne.Shortcut) bool {
	if _, copies := s.(*fyne.ShortcutCopy); copies {
		// Ctrl+C with the prompt focused but nothing selected in it: the
		// text selected in the conversation is what is meant.
		return (u.prompt == nil || u.prompt.SelectedText() == "") && u.copySelection()
	}
	c, ok := s.(*desktop.CustomShortcut)
	if !ok {
		return false
	}
	switch {
	case c.KeyName == fyne.KeyK && c.Modifier == fyne.KeyModifierControl:
		u.startNew()
	case c.KeyName == fyne.KeyR && c.Modifier == fyne.KeyModifierControl|fyne.KeyModifierShift:
		u.restart()
	case c.KeyName == fyne.KeyB && c.Modifier == fyne.KeyModifierControl:
		u.toggleSidebar()
	default:
		return false
	}
	return true
}

// release stops background work and saves, before closing or restarting.
func (u *desktopUI) release() {
	u.shutdown() // every run's context derives from it
	u.panel.dispose()
	u.remote.close()
	u.mark.stop()
	u.workMark.stop()
	u.persist(true)
}

// fitLabel is a single line of text that ellipsises to the space it gets.
type fitLabel struct {
	widget.BaseWidget
	text  string
	size  float32
	color color.Color
	label *canvas.Text
}

func newFitLabel(text string, size float32, c color.Color) *fitLabel {
	l := &fitLabel{text: text, size: size, color: c, label: textLine(text, size, c)}
	l.ExtendBaseWidget(l)
	return l
}

func (l *fitLabel) SetText(text string) {
	if text == l.text {
		return
	}
	l.text = text
	l.Refresh()
}

func (l *fitLabel) CreateRenderer() fyne.WidgetRenderer { return &fitLabelRenderer{l} }

type fitLabelRenderer struct{ l *fitLabel }

func (r *fitLabelRenderer) Layout(size fyne.Size) {
	r.l.label.Text = fitText(r.l.text, r.l.size, fyne.TextStyle{}, size.Width)
	r.l.label.Move(fyne.NewPos(0, (size.Height-r.l.label.MinSize().Height)/2))
}
func (r *fitLabelRenderer) MinSize() fyne.Size {
	return fyne.NewSize(24, fyne.MeasureText("Ag", r.l.size, fyne.TextStyle{}).Height)
}
func (r *fitLabelRenderer) Refresh() {
	r.l.label.Color = r.l.color
	r.Layout(r.l.Size())
	r.l.label.Refresh()
}
func (r *fitLabelRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.l.label} }
func (r *fitLabelRenderer) Destroy()                     {}

// flatScrollTheme drops the scroll edge shadows: the conversation fades
// under the composer instead of ending on a hard line.
type flatScrollTheme struct{ fyne.Theme }

func (t flatScrollTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameShadow {
		return color.Transparent
	}
	return t.Theme.Color(name, v)
}

func (u *desktopUI) build() fyne.CanvasObject {
	u.tips = newTipLayer()
	u.buildSettings()
	sidebar := u.buildSidebar()

	// Conversation column. While the model works, the animated ring sits
	// under the last message.
	afterTypeset = func() {
		if u.ctx.Err() == nil {
			u.relayoutColumn()
		}
	}
	u.items = &transcriptLayout{u: u}
	u.items.reset()
	u.transcript = container.New(u.items)
	u.views, u.copyRows = map[int]any{}, map[int]*fyne.Container{}
	u.workMark = newLoopMark(inlineMarkSize)
	u.thinkLabel = newFitLabel("", 14, colMuted)
	u.thinking = container.NewBorder(nil, nil, container.NewCenter(u.workMark.image), nil, padded(0, 0, 12, 0, u.thinkLabel))
	u.inlineSlot = container.NewStack(u.thinking)
	u.inlineRow = padded(6, 0, 4, 0, u.inlineSlot)
	u.inlineRow.Hide()
	u.dockRow = container.NewStack()
	u.dockRow.Hide()
	u.column = container.NewVBox(u.transcript, u.inlineRow)
	u.scroll = newKineticVScroll(container.New(columnLayout{width: 760, inset: 24}, u.column))
	u.followTail = true
	u.scroll.OnScrolled = func(position fyne.Position) {
		// A small tolerance absorbs fractional pixel rounding at the bottom.
		// Content.Size, not MinSize: MinSize walks the whole chat on every event.
		bottom := fyne.Max(0, u.scroll.Content.Size().Height-u.scroll.Size().Height)
		u.followTail = bottom-position.Y <= 2
		relayout(u.transcript) // items entering the view come back, those leaving it rest
	}
	u.mark = newLoopMark(60)
	u.welcome = container.NewCenter(container.NewVBox(
		container.NewCenter(u.mark.image), spacer(0, 18),
		container.NewCenter(textLine("What shall we build today?", 24, colText)), spacer(0, 4),
		container.NewCenter(textLine("Your project. Your model. One loop.", 14, colMuted)), spacer(0, 40)))
	// Nothing is drawn between the conversation and the composer: no rule,
	// no fade (a gradient of the background over itself showed as darker
	// bands, 8 bits being too few for so dark a colour). The messages just
	// end where the composer's block begins.
	scroll := container.NewThemeOverride(u.scroll, flatScrollTheme{loopTheme{theme.DefaultTheme()}})
	view := container.NewStack(u.welcome, scroll)
	u.scroll.Hide()

	conversation := container.NewBorder(nil, u.buildFooter(), nil, nil, view)
	// The activity line may sit beside the composer (dockActivity): the
	// dock layer floats over the conversation's bottom left corner.
	conversation = container.New(dockLayout{u}, conversation, u.dockRow)
	u.panel = newToolPanel(u)
	u.split = container.New(splitLayout{u.panel}, conversation, u.panel.view, newPanelResizeHandle(u.panel))

	// Title bar: conversation title, drag region and window controls.
	u.header = newFitLabel("New chat", 14, colMuted)
	u.expand = newRow("", iconSidebar, styleWindow, u.toggleSidebar)
	u.expand.Hide()
	u.panelToggle = newRow("", iconPanel, styleWindow, func() {
		if u.panel.target > 0 {
			u.panel.fold()
		} else {
			u.panel.unfold()
		}
	})
	u.panelToggle.onHover = func(in bool) {
		if in {
			u.tips.show(u.panelToggle, "Toggle side panel")
		} else {
			u.tips.hide()
		}
	}
	right := container.New(layout.NewCustomPaddedHBoxLayout(2), container.NewCenter(u.panelToggle))
	if u.chrome {
		right.Add(windowControls(u.window, u.quit))
	}
	u.headerBar = container.NewBorder(nil, nil, padded(0, 0, 0, 8, container.NewCenter(u.expand)), right, u.header)
	var header fyne.CanvasObject = padded(14, 8, 24, 24, u.headerBar)
	if u.chrome {
		header = container.NewStack(newDragArea(u.window), padded(8, 4, 20, 8, u.headerBar))
	}
	// The settings page takes the conversation's place while it is open.
	main := container.NewStack(canvas.NewRectangle(colBackground), container.NewBorder(header, nil, nil, nil, container.NewStack(u.split, u.prefs.view)))
	// No rule between the panes: the sidebar only casts a faint shadow.
	shade := canvas.NewHorizontalGradient(color.NRGBA{A: 0x2a}, color.NRGBA{})
	u.pane = &paneLayout{shown: 1, shade: 14}
	u.root = container.New(u.pane, sidebar, main, shade)
	layers := []fyne.CanvasObject{u.root}
	if u.chrome {
		layers = append(layers, resizeEdges(u.window))
	}
	return container.NewStack(append(layers, u.tips.layer)...)
}

// buildFooter assembles the context line, the queue tray and the composer
// card: the card's own outline is the only edge of the input area.
func (u *desktopUI) buildFooter() fyne.CanvasObject {
	u.prompt = newChatEntry()
	u.prompt.SetPlaceHolder("Describe what you want to build…")
	u.prompt.send = u.submit
	u.prompt.steer = u.steerNow
	u.prompt.escape = func() { u.finishEdit(false); u.clearSelection() }
	u.window.Canvas().SetOnTypedKey(func(key *fyne.KeyEvent) {
		if key.Name == fyne.KeyEscape && u.prefs.open { // Escape goes back a page, then to the conversation
			u.prefs.back()
		}
	})
	u.prompt.shortcut = u.shortcut
	u.prompt.paste = u.pasteFromClipboard

	u.model = newPicker(u.window, "Loading models…", styleChip, func(label string) {
		if choice, ok := u.choices[label]; ok {
			u.selectModel(choice)
		}
	})
	u.model.row.onTap = u.modelMenu
	u.send = newIconButton(iconSend, colAmber, colInk, u.submit)
	u.send.iconPx = 20
	u.stop = newIconButton(iconStop, colText, colInk, func() {
		if u.current != nil && u.current.run != nil {
			devf("ui", "stop pressed for %q", u.current.Title)
			u.current.run.cancel()
		}
	})
	u.stop.iconPx = 20
	u.stop.Hide()
	u.approvalControl = newRow("", iconShield, styleChip, u.approvalMenu)
	u.updateApprovalControl()
	u.contextRing = newContextRing(u.tips)
	u.attach = newIconButton(iconPlus, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0d}, color.NRGBA{}, nil)
	u.attach.iconPx, u.attach.padPx = 18, 6
	u.attach.onTap = func() {
		showMenu(u.window, u.attach, []menuAction{
			{label: "Paste image", detail: "From the clipboard", icon: iconCopy, run: func() {
				if !u.pasteFromClipboard() {
					u.notice("Paste", "No image in the clipboard.")
				}
			}},
			{label: "Attach images…", detail: "Choose files on this computer", icon: iconFolder, run: u.pickImages},
		})
	}

	actions := container.New(layout.NewCustomPaddedHBoxLayout(4), u.model.row, u.contextRing, container.NewStack(u.send, u.stop))
	u.controls = container.New(&composerControlsLayout{}, container.NewCenter(u.approvalControl), actions)
	u.model.relayout = u.controls.Refresh

	card := canvas.NewRectangle(colSurface)
	card.CornerRadius = 16
	card.StrokeColor = colBorder
	card.StrokeWidth = 1
	u.prompt.onFocus = func(focused bool) {
		from, to := colBorder, colBorderHot
		if !focused {
			from, to = to, from
		}
		canvas.NewColorRGBAAnimation(from, to, 160*time.Millisecond, func(c color.Color) {
			card.StrokeColor = c
			card.Refresh()
		}).Start()
	}
	u.attachRow = container.New(layout.NewCustomPaddedHBoxLayout(8))
	// The optional rows are hidden with their margins: a hidden child inside
	// a visible padded box would still take its padding and the gap after it.
	u.attachTray = padded(2, 4, 2, 0, u.attachRow)
	u.attachTray.Hide()
	input := container.NewThemeOverride(u.prompt, bareInputTheme{loopTheme{theme.DefaultTheme()}})
	line := container.New(attachButtonLayout{}, input, u.attach)
	pencil := canvas.NewImageFromResource(tinted(iconPencil, colAmber))
	pencil.SetMinSize(fyne.NewSquareSize(14))
	cancelEdit := newRow("Cancel", nil, styleChip, func() { u.finishEdit(false) })
	cancelEdit.size = 12.5
	u.editBar = padded(0, 0, 4, 0, container.NewBorder(nil, nil,
		container.NewHBox(container.NewCenter(pencil), textLine("Editing a queued message", 12.5, colAmber)),
		container.NewHBox(container.NewCenter(textLine("Enter to save · Esc to cancel", 12, colFaint)), cancelEdit)))
	u.editBar.Hide()
	composer := container.NewStack(card, padded(8, 8, 10, 10, container.NewVBox(u.editBar, u.attachTray, line, u.controls)))
	u.composer = composer

	// Messages waiting for the running task sit in a tray on top of the card.
	u.queueTitle = newFitLabel("", 12.5, colMuted)
	clear := newRow("Clear", nil, styleChip, u.clearQueue)
	clear.size = 12.5
	u.queueList = container.New(layout.NewCustomPaddedVBoxLayout(0))
	trayBg := canvas.NewRectangle(colPopover)
	trayBg.TopLeftCornerRadius, trayBg.TopRightCornerRadius = 12, 12
	trayBg.StrokeColor = colBorderSoft
	trayBg.StrokeWidth = 1
	trayBody := container.NewVBox(container.NewBorder(nil, nil, nil, clear, padded(0, 0, 8, 0, u.queueTitle)), u.queueList)
	u.queueTray = padded(0, 0, 16, 16, container.NewStack(trayBg, padded(4, 6, 4, 4, trayBody)))
	u.queueTray.Hide()

	u.folder = newRow(filepath.Base(u.cfg.Workspace), theme.FolderIcon(), styleChip, nil)
	u.folder.onTap = func() { u.projectMenu(u.folder) }
	machine := newRow("This computer", theme.ComputerIcon(), styleChip, nil)
	contextRow := container.NewHBox(u.folder, machine)
	stack := container.New(layout.NewCustomPaddedVBoxLayout(0), padded(0, 6, 4, 4, contextRow), u.queueTray, composer)
	return padded(0, 0, 24, 24, container.New(columnLayout{width: 760, inset: 0}, padded(0, 18, 0, 0, stack)))
}

func (u *desktopUI) updateHeader() {
	u.folder.SetText(filepath.Base(u.cfg.Workspace))
	title := "New chat"
	if u.current != nil {
		title = u.current.Title
	}
	if u.prefs != nil && u.prefs.open {
		title = "Settings"
	}
	u.header.SetText(title)
}

func (u *desktopUI) startNew() {
	devf("ui", "new chat screen (workspace %s)", u.cfg.Workspace)
	u.prefs.close()
	u.clearSelection()
	u.finishEdit(false)
	u.current = nil
	u.cfg.Session = ""
	u.followTail = true
	u.transcript.RemoveAll()
	u.items.reset()
	u.views, u.copyRows = map[int]any{}, map[int]*fyne.Container{}
	u.panel.switchTo(nil)
	u.showTranscript(false)
	u.syncRun()
	u.mark.intro()
	u.updateHeader()
	u.renderChats()
	u.refreshContextRing()
	u.window.Canvas().Focus(u.prompt)
}

// showTranscript swaps the welcome screen and the conversation.
func (u *desktopUI) showTranscript(show bool) {
	if show {
		u.welcome.Hide()
		u.scroll.Show()
	} else {
		u.scroll.Hide()
		u.welcome.Show()
	}
}

func codeText(text string) fyne.CanvasObject {
	if strings.HasPrefix(text, "--- ") || strings.HasPrefix(text, "diff --git ") {
		var segments []widget.RichTextSegment
		for _, line := range strings.SplitAfter(text, "\n") {
			name := theme.ColorNameForeground
			if strings.HasPrefix(line, "+") {
				name = theme.ColorNameSuccess
			} else if strings.HasPrefix(line, "-") {
				name = theme.ColorNameError
			} else if strings.HasPrefix(line, "@@") {
				name = theme.ColorNamePlaceHolder
			}
			segments = append(segments, &widget.TextSegment{Text: line, Style: widget.RichTextStyle{Inline: true, ColorName: name, TextStyle: fyne.TextStyle{Monospace: true}}})
		}
		rich := widget.NewRichText(segments...)
		rich.Wrapping = fyne.TextWrapBreak
		return rich
	}
	l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Monospace: true})
	l.Selectable = true
	l.Wrapping = fyne.TextWrapBreak
	return l
}

// codeBlock frames code in a dark well that scrolls past a sensible height.
func codeBlock(text string, maxHeight float32) fyne.CanvasObject {
	code := codeText(text)
	scroll := container.NewScroll(code)
	lines := float32(strings.Count(text, "\n") + 1)
	line := fyne.MeasureText("Ag", theme.TextSize(), fyne.TextStyle{Monospace: true}).Height + theme.LineSpacing()
	scroll.SetMinSize(fyne.NewSize(0, fyne.Min(maxHeight, fyne.Max(48, lines*line+16))))
	return surface(scroll, colCode, colBorderSoft, 10, 4)
}

// modal opens a themed sheet with a title, a body and right-aligned actions.
// With no actions it shows a single "Close" button.
func (u *desktopUI) modal(title string, body fyne.CanvasObject, width float32, onDismiss func(), actions ...*tapRow) *sheet {
	var s *sheet
	closeButton := newRow("", iconClose, styleWindow, func() { s.dismiss() })
	heading := textLine(title, 16, colText)
	heading.TextStyle = fyne.TextStyle{Bold: true}
	if len(actions) == 0 {
		actions = []*tapRow{newRow("Close", nil, styleOutline, func() { s.dismiss() })}
	}
	buttons := container.New(layout.NewCustomPaddedHBoxLayout(8), layout.NewSpacer())
	for _, action := range actions {
		buttons.Add(action)
	}
	content := container.NewBorder(
		padded(0, 12, 0, 0, container.NewBorder(nil, nil, nil, closeButton, container.NewHBox(heading))),
		padded(16, 0, 0, 0, buttons), nil, nil, body)
	s = newSheet(u.window.Canvas(), content, width)
	s.onDismiss = onDismiss
	s.Show()
	u.window.Canvas().Focus(s)
	return s
}

func (u *desktopUI) notice(title, text string) {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	u.modal(title, label, 420, nil)
}

// syncRun shows the open chat's run state: the stop button, the activity
// line under the conversation and the waiting messages.
func (u *desktopUI) syncRun() {
	if u.settled != nil {
		u.settled.Stop()
		u.settled = nil
	}
	var t *turn
	if u.current != nil && u.current.run != nil {
		t = u.current.run.turn
	}
	if t != nil {
		u.mark.stop()
		u.send.Hide()
		u.stop.Show()
		u.showActivityLine(true)
		u.applyActivity(t)
	} else {
		u.send.Show()
		u.stop.Hide()
		u.showActivityLine(false)
		u.workMark.setState("brand")
	}
	u.controls.Refresh()
	u.renderQueue()
	u.syncCopies()
	u.relayoutColumn()
}

// runStarted marks a chat as working.
func (u *desktopUI) runStarted(chat *conversation, run *chatRun) {
	devf("run", "start %q [%s]: %s %s, effort %q, approval %q, session %q, on screen %v",
		chat.Title, chat.ID, chat.Config.Provider, chat.Config.Model, chat.Config.Effort, approvalFor(u.approvalMode, chat.Config), chat.Config.Session, chat == u.current)
	run.started = time.Now()
	chat.run = run
	u.remote.metaChanged(chat)
	if chat == u.current {
		u.syncRun()
	}
	u.renderChats()
}

// runEnded closes a chat's run. ended is the ring shown briefly under the
// answer when the chat is on screen: "done" or "stopped".
func (u *desktopUI) runEnded(chat *conversation, ended string) {
	run := chat.run
	if run != nil {
		devf("run", "end %q [%s]: %s after %s, on screen %v", chat.Title, chat.ID, ended, time.Since(run.started).Round(time.Millisecond), chat == u.current)
	}
	chat.run = nil
	u.remote.metaChanged(chat)
	if run != nil && run.approval != nil {
		run.approval.dismiss()
	}
	u.renderChats()
	u.save()
	if chat != u.current {
		return
	}
	u.syncRun()
	u.refreshContextRing()
	// The closed ring stays briefly under the answer, then goes away.
	u.showActivityLine(true)
	u.workMark.setState(ended)
	u.thinkLabel.SetText(map[string]string{"done": "Done", "stopped": "Stopped"}[ended])
	u.relayoutColumn()
	u.settled = time.AfterFunc(1600*time.Millisecond, func() {
		fyne.Do(func() {
			if chat == u.current && chat.run == nil {
				u.showActivityLine(false)
				u.workMark.setState("brand")
				u.relayoutColumn()
			}
		})
	})
	if u.window.Canvas().Overlays().Top() == nil { // never pull focus out of an open dialog
		u.window.Canvas().Focus(u.prompt)
	}
}

// promptMessage takes what the composer holds as a message.
func (u *desktopUI) promptMessage() (message, bool) {
	text := strings.TrimSpace(u.prompt.Text)
	if text == "" && len(u.attachments) == 0 {
		return message{}, false
	}
	m := message{Role: "user", Content: text, Images: u.takeAttachments()}
	u.prompt.SetText("")
	return m, true
}

// submit is Enter: send, or queue the message behind the running task.
func (u *desktopUI) submit() {
	if u.editing != nil {
		u.finishEdit(true)
		return
	}
	text := strings.TrimSpace(u.prompt.Text)
	chat := u.current
	if chat != nil && chat.run != nil {
		if m, ok := u.promptMessage(); ok {
			chat.queue = append(chat.queue, m)
			u.renderQueue()
		}
		return
	}
	if text == "" && len(u.attachments) == 0 {
		// An empty Enter resumes a queue left behind by a stopped task.
		if chat != nil {
			if next, ok := u.takeNext(chat); ok {
				u.start(next)
			}
		}
		return
	}
	// Claude Code compacts its own session: there /compact is sent as it is.
	if u.cfg.Provider != "claudecode" && (text == "/compact" || strings.HasPrefix(text, "/compact ")) {
		u.prompt.SetText("")
		u.compactChat(strings.TrimSpace(strings.TrimPrefix(text, "/compact")))
		return
	}
	if !u.ready() {
		return
	}
	if m, ok := u.promptMessage(); ok {
		u.start(m)
	}
}

// steerNow is Ctrl+Enter: the message joins the running task at its next
// step, without waiting for the answer to end.
func (u *desktopUI) steerNow() {
	chat := u.current
	if u.editing != nil || chat == nil || chat.run == nil {
		u.submit()
		return
	}
	if m, ok := u.promptMessage(); ok {
		chat.steer = append(chat.steer, m)
		u.renderQueue()
	}
}

// ready checks that a message can be sent now, and says why not.
func (u *desktopUI) ready() bool {
	if problem := u.notReady(); problem != "" {
		u.notice("Not ready", problem)
		return false
	}
	return true
}

// notReady is why a message cannot be sent now ("" when it can).
func (u *desktopUI) notReady() string {
	if u.modelsLoading {
		return "The models are still loading."
	}
	if u.cfg.Model == "" {
		return "Choose a connection and a model."
	}
	root, err := resolveWorkspace(u.cfg.Workspace)
	if err != nil {
		return err.Error()
	}
	u.cfg.Workspace = root
	return ""
}

// start sends a message in the open chat (a new one if none is open) and
// runs the agent loop for it in the background.
func (u *desktopUI) start(m message) { u.startWithID(m, "") }

func (u *desktopUI) startWithID(m message, id string) {
	if !u.ready() {
		if u.current != nil { // keep it rather than lose it
			u.current.queue = append([]message{m}, u.current.queue...)
			u.renderQueue()
		}
		return
	}
	chat := u.current
	if chat == nil {
		title := []rune(strings.Join(strings.Fields(m.Content), " "))
		if len(title) > 36 {
			title = title[:36]
		}
		if len(title) == 0 {
			title = []rune("[image]")
		}
		u.cfg.Session = "" // a new chat starts its own Claude Code session
		chat = &conversation{ID: identifier()[:16], Title: string(title), Config: u.cfg}
		u.chats = append([]*conversation{chat}, u.chats...)
		u.current = chat
		u.panel.adopt(chat) // what was open on the start screen is now this chat's
	}
	u.panel.todoChanged(chat)
	chat.History = append(chat.History, m)
	u.showTranscript(true)
	u.followTail = true // Sending a message explicitly returns to the live answer.
	u.addItem(chat, chatItem{ID: id, Kind: "user", Text: m.Content, Images: m.Images})
	u.updateHeader()
	// No deadline: a long task (long shell runs, many tool rounds) must not
	// die on a timer. Only the Stop button or a dead network ends it.
	ctx, cancel := context.WithCancel(u.ctx)
	cfg := u.runConfig(chat)
	history := append([]message(nil), chat.History...)
	t := u.newTurn(chat, cfg)
	t.show(activity{State: "wait"})
	u.runStarted(chat, &chatRun{cancel: cancel, turn: t})
	pump := &eventPump{handle: t.handle}
	in := inbox{
		steer: func() (out []message) {
			u.onUI(ctx, func() { out = u.takeSteer(chat) })
			return out
		},
		next: func() (m message, ok bool) {
			u.onUI(ctx, func() { m, ok = u.takeNext(chat) })
			return m, ok
		},
	}
	go func() {
		defer cancel()
		err := u.backend.run(ctx, cfg, history, in, pump.emit)
		canceled := ctx.Err() == context.Canceled
		fyne.Do(func() {
			pump.flush() // whatever the stream sent last comes first
			if u.ctx.Err() != nil {
				return
			}
			t.finish()
			ended := "done"
			if err != nil {
				ended = "stopped"
				text := err.Error()
				if canceled {
					text = "Response stopped."
				}
				u.addItem(chat, chatItem{Kind: "error", Text: text})
			}
			u.runEnded(chat, ended)
		})
	}()
}

// runConfig is the chat's configuration with this session's secrets and
// preferences, for one run.
func (u *desktopUI) runConfig(chat *conversation) config {
	chat.identifyMessages()
	if chat.Todo == nil {
		chat.Todo = &todoState{}
	}
	cfg := chat.Config
	cfg.Todo = chat.Todo
	if chat.Cache == nil {
		chat.Cache = &contextCache{}
	}
	cfg.Cache = chat.Cache
	cfg.ContextKey = chat.ID + ":" + chat.Version
	cfg.Recall = func(ctx context.Context) ([]chatItem, error) {
		var items []chatItem
		u.onUI(ctx, func() { items = append([]chatItem{}, chat.Items...) })
		return items, ctx.Err()
	}
	cfg.APIKey = u.keys[cfg.ConnectionID]
	cfg.UserInstructions = u.userInstructions
	cfg.ApprovalMode = approvalFor(u.approvalMode, cfg)
	cfg.ClassicCompaction = u.classicCompaction
	return cfg
}

// onUI runs f on the UI thread and waits, unless the run is over.
func (u *desktopUI) onUI(ctx context.Context, f func()) {
	if ctx.Err() == nil { // closing: never block the loop on the window
		fyne.DoAndWait(f)
	}
}

// compactChat runs the manual /compact command, optionally steered by a
// focus ("/compact keep the API design").
func (u *desktopUI) compactChat(focus string) {
	chat := u.current
	if chat == nil || len(chat.History) == 0 {
		u.notice("Compact", "Nothing to compact yet.")
		return
	}
	if !u.ready() {
		return
	}
	ctx, cancel := context.WithCancel(u.ctx)
	cfg := u.runConfig(chat)
	history := append([]message(nil), chat.History...)
	t := u.newTurn(chat, cfg)
	t.show(activity{State: "compact"})
	u.runStarted(chat, &chatRun{cancel: cancel, turn: t})
	pump := &eventPump{handle: t.handle}
	go func() {
		defer cancel()
		kept, err := u.backend.compactNow(ctx, cfg, history, focus, pump.emit)
		fyne.Do(func() {
			pump.flush()
			if u.ctx.Err() != nil {
				return
			}
			ended := "done"
			if err != nil {
				ended = "stopped"
				u.addItem(chat, chatItem{Kind: "error", Text: err.Error()})
			} else {
				chat.History = kept
				u.addItem(chat, chatItem{Kind: "note", Text: "Context compacted: the earlier rounds were condensed"})
			}
			u.runEnded(chat, ended)
		})
	}()
}

// showApproval asks before a command or an edit, whichever chat it comes
// from: the run waits for the answer.
func (u *desktopUI) showApproval(chat *conversation, p map[string]any) {
	id, _ := p["id"].(string)
	title, _ := p["title"].(string)
	preview, _ := p["preview"].(string)
	if chat != u.current {
		title += " · " + chat.Title
	}
	resolved := false
	var s *sheet
	forget := func() {
		if chat.run != nil && chat.run.approval == s {
			chat.run.approval = nil
			chat.run.approvalID, chat.run.answer = "", nil
			u.remote.metaChanged(chat)
		}
	}
	answer := func(allow bool) {
		if resolved {
			return
		}
		resolved = true
		u.backend.approve(id, allow)
		forget()
		s.Hide()
	}
	refuse := newRow("Deny", nil, styleOutline, func() { answer(false) })
	allow := newRow("Allow", nil, stylePrimary, func() { answer(true) })
	intro := textLine("1loop needs your approval to perform this action.", 14, colMuted)
	body := container.NewBorder(padded(0, 10, 0, 0, intro), nil, nil, nil, codeBlock(preview, 360))
	s = u.modal(title, body, 680, func() {
		if !resolved {
			resolved = true
			u.backend.approve(id, false)
			forget()
		}
	}, refuse, allow)
	if chat.run != nil {
		chat.run.approval = s
		chat.run.approvalID, chat.run.approvalTitle, chat.run.approvalPreview = id, title, preview
		chat.run.answer = answer
		u.remote.metaChanged(chat)
	}
}

func (u *desktopUI) refreshModels() {
	u.modelLoad++
	generation := u.modelLoad
	previous := u.cfg
	u.modelsLoading = true
	connections := append([]connection(nil), u.connectionsList...)
	keys := make(map[string]string, len(u.keys))
	for id, key := range u.keys {
		keys[id] = key
	}
	u.model.Disable()
	u.model.placeholder = "Loading models…"
	u.model.ClearSelected()
	u.model.row.SetTrailing("")
	u.send.Disable()
	go func() {
		type result struct {
			choices []modelChoice
			err     error
		}
		results := make([]result, len(connections))
		var wg sync.WaitGroup
		for i, c := range connections {
			if !c.Enabled {
				continue
			}
			wg.Add(1)
			go func(i int, c connection) {
				defer wg.Done()
				results[i].choices, results[i].err = u.backend.connectionModels(u.ctx, c, keys[c.ID])
				var timeout net.Error
				if c.Provider == "openai" && errors.As(results[i].err, &timeout) && timeout.Timeout() && u.ctx.Err() == nil {
					results[i].choices, results[i].err = u.backend.connectionModels(u.ctx, c, keys[c.ID])
				}
			}(i, c)
		}
		wg.Wait()
		fyne.Do(func() {
			if u.ctx.Err() != nil || generation != u.modelLoad {
				return
			}
			u.modelsLoading = false
			u.choices = map[string]modelChoice{}
			u.model.Details = map[string]string{}
			u.modelUnavailable = map[string]string{}
			var options []string
			selected := ""
			for i, result := range results {
				if result.err != nil {
					problem := "Models unavailable"
					if connections[i].Provider == "vllm" {
						problem = "Unavailable"
					} else {
						var timeout net.Error
						if errors.As(result.err, &timeout) && timeout.Timeout() {
							problem = "Network timeout"
						}
					}
					u.modelUnavailable[connections[i].ID] = problem
					continue
				}
				for _, choice := range result.choices {
					if !choice.Connection.includes(choice.ID) {
						continue
					}
					name := choice.Name
					if runes := []rune(name); len(runes) > 38 {
						name = string(runes[:38]) + "…"
					}
					label := name + " · " + choice.Connection.Name
					if _, exists := u.choices[label]; exists {
						label += fmt.Sprintf(" · %d", len(options)+1)
					}
					options = append(options, label)
					u.choices[label] = choice
					u.model.Details[label] = choice.Connection.Name + " · " + choice.ID
					if choice.Connection.Provider == "claudecode" {
						u.model.Details[label] = "Runs in Claude Code's own harness · " + choice.ID
					}
					if choice.Connection.ID == previous.ConnectionID && choice.ID == previous.Model {
						selected = label
					}
				}
			}
			u.model.Options = options
			u.remote.settingsChanged()
			u.model.Enable()
			if len(options) == 0 {
				u.cfg.Model = ""
				u.model.placeholder = "No model available"
				u.model.refresh()
				return
			}
			if selected == "" {
				selected = options[0]
			}
			u.model.SetSelected(selected)
			u.send.Enable()
			u.refreshContextRing()
		})
	}()
}

// field stacks a small caption above an input, like a web form.
func field(caption string, input fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(padded(4, 0, 2, 0, textLine(caption, 13, colMuted)), input)
}

type savedState struct {
	ClaudeOffered    bool            `json:"claudeOffered,omitempty"`
	RemoteEnabled    *bool           `json:"remoteEnabled,omitempty"`
	Devices          []pairedDevice  `json:"devices,omitempty"`
	Config           config          `json:"config"`
	Chats            []*conversation `json:"chats"`
	Projects         []string        `json:"projects"`
	Connections      []connection    `json:"connections"`
	UserInstructions string          `json:"userInstructions"`
	ApprovalMode     string          `json:"approvalMode"`
	PanelWidth       float32         `json:"panelWidth,omitempty"`
	// Saved as its opposite so that a state file without it means dynamic.
	ClassicCompaction bool `json:"classicCompaction,omitempty"`
}

// save writes the state shortly after the last change, so bursts of
// changes (a streamed turn, a slider drag) cost one write.
func (u *desktopUI) save() {
	if u.saveTimer != nil {
		u.saveTimer.Stop()
	}
	u.saveTimer = time.AfterFunc(400*time.Millisecond, func() { fyne.Do(u.saveNow) })
}

func (u *desktopUI) saveNow() { u.persist(false) }

// persist writes the state file. The conversations are nearly all of it
// (megabytes once they hold tool outputs and images), so they are encoded
// and written beside the interface, from copies taken here: a save used to
// freeze the window for as long as it took. wait is for closing, when the
// file must be on disk before the process ends.
func (u *desktopUI) persist(wait bool) {
	started := time.Now()
	if u.saveTimer != nil {
		u.saveTimer.Stop()
		u.saveTimer = nil
	}
	remote := u.remoteEnabled
	// Everything but the chats is small, and holds maps the settings edit:
	// it is encoded here, on the interface's thread.
	head, err := json.Marshal(savedState{u.claudeOffered, &remote, u.devices, u.cfg, nil, u.projects, u.connectionsList, u.userInstructions, u.approvalMode, u.panelWidth, u.classicCompaction})
	fields := map[string]json.RawMessage{}
	if err == nil {
		err = json.Unmarshal(head, &fields)
	}
	if u.panel != nil {
		u.panel.stash() // the open chat's panel, as it is now
	}
	chats := make([]*conversation, len(u.chats))
	for i, chat := range u.chats { // messages are never edited in place: copying the lists is enough
		chats[i] = &conversation{ID: chat.ID, Title: chat.Title, Config: chat.Config, Panel: chat.Panel,
			History: append([]message{}, chat.History...), Items: append([]chatItem{}, chat.Items...), Todo: chat.Todo.copy(), Cache: chat.Cache.copy(),
			Version: chat.Version, Versions: copyVersions(chat.Versions)}
	}
	u.saveCount++
	count, path := u.saveCount, u.statePath
	write := func() {
		u.saveLock.Lock()
		defer u.saveLock.Unlock()
		if count < u.saveDone {
			return // a newer state is already on disk
		}
		u.saveDone = count
		var data []byte
		if err == nil {
			fields["chats"], err = json.Marshal(chats)
		}
		if err == nil {
			data, err = json.Marshal(fields)
		}
		if err == nil {
			err = os.MkdirAll(filepath.Dir(path), 0700)
		}
		if err == nil {
			// Write then rename, so a crash never leaves half a state file.
			tmp := path + ".tmp"
			if err = os.WriteFile(tmp, data, 0600); err == nil {
				err = os.Rename(tmp, path)
			}
		}
		devf("app", "state saved: %d chats, %d KiB, %s in all (error: %v)", len(chats), len(data)>>10, time.Since(started).Round(time.Millisecond), err)
		report := func() {
			if err != nil && err.Error() != u.saveError && u.ctx.Err() == nil {
				u.notice("Unable to save", err.Error()) // once per distinct problem
			}
			u.saveError = ""
			if err != nil {
				u.saveError = err.Error()
			}
		}
		if wait {
			report()
		} else {
			fyne.Do(report)
		}
	}
	if wait {
		write()
	} else {
		go write()
	}
}

func (u *desktopUI) load() {
	u.remoteEnabled = true // the remote is on until the user turns it off
	defer u.remoteDefaults()
	data, err := os.ReadFile(u.statePath)
	if err != nil {
		return
	}
	var state savedState
	if json.Unmarshal(data, &state) != nil {
		return
	}
	if state.RemoteEnabled != nil {
		u.remoteEnabled = *state.RemoteEnabled
	}
	u.devices = state.Devices
	u.panelWidth = state.PanelWidth
	u.claudeOffered = state.ClaudeOffered
	if state.Config.Provider == "codex" {
		state.Config.Provider = "openai"
		state.Config.Model = ""
		state.Config.BaseURL = ""
	}
	switch state.Config.Provider {
	case "vllm", "openrouter", "openai", "compatible", "claudecode":
	default:
		return
	}
	state.Config.APIKey = ""
	u.cfg = state.Config
	u.projects = state.Projects
	u.connectionsList = state.Connections
	u.userInstructions = state.UserInstructions
	u.approvalMode = state.ApprovalMode
	u.classicCompaction = state.ClassicCompaction
	for _, chat := range state.Chats {
		if chat == nil {
			continue
		}
		if chat.Config.Provider == "codex" {
			chat.Config.Provider = "openai"
		}
		chat.Config.APIKey = ""
		chat.Items = upgradeItems(chat.Items)
		chat.todoCount = len(chat.Todo.snapshot())
		u.chats = append(u.chats, chat)
		u.ensureProject(chat.Config.Workspace)
	}
}

// ---------------------------------------------------------------------------
// Activity line placement.
//
// dockActivity puts the activity line (the work ring and what the agent is
// doing) in the empty space left of the composer, with a bigger ring, when
// the window leaves room for it there; otherwise it stays under the last
// message. Set it to false to always keep it under the last message, as
// before (patches/dock-activity.patch removes the feature entirely).
const dockActivity = true

const (
	inlineMarkSize = 40  // the ring under the last message
	dockMarkSize   = 64  // the ring beside the composer
	dockMinWidth   = 200 // narrower than this, the line stays under the message
)

// dockLayout lays the conversation out and floats the dock beside the
// composer, in the margin left of the conversation column.
type dockLayout struct{ u *desktopUI }

func (l dockLayout) MinSize(objects []fyne.CanvasObject) fyne.Size { return objects[0].MinSize() }

func (l dockLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.Position{})
	objects[0].Resize(size)
	// The footer centres a 760 wide column between 24 px margins (buildFooter).
	column := fyne.Min(760, size.Width-48)
	left := 24 + (size.Width-48-column)/2
	width := left - 24 - 24 // a margin on each side of the dock
	card := l.u.composer.Size().Height
	objects[1].Move(fyne.NewPos(24, size.Height-18-card))
	objects[1].Resize(fyne.NewSize(fyne.Max(0, width), card))
	if docked := dockActivity && width >= dockMinWidth; docked != l.u.docked {
		fyne.Do(func() { l.u.setDocked(docked) }) // not while laying out
	}
}

// setDocked moves the activity line between its two places.
func (u *desktopUI) setDocked(docked bool) {
	if docked == u.docked {
		return
	}
	u.docked = docked
	from, to, size := u.inlineSlot, u.dockRow, float32(dockMarkSize)
	if !docked {
		from, to, size = u.dockRow, u.inlineSlot, inlineMarkSize
	}
	from.Remove(u.thinking)
	to.Add(u.thinking)
	u.workMark.resize(size)
	u.showActivityLine(u.lineShown)
}

// showActivityLine shows or hides the activity line wherever it is.
func (u *desktopUI) showActivityLine(show bool) {
	u.lineShown = show
	if show && u.docked {
		u.dockRow.Show()
	} else {
		u.dockRow.Hide()
	}
	if show && !u.docked {
		u.inlineRow.Show()
	} else {
		u.inlineRow.Hide()
	}
	u.relayoutColumn()
}
