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
	computer         *computerSession
	browser          *browserEngine
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
	fileAttachments []fileAttachment
	filesAttaching  int
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
	cheapCacheClock()
	a := fyneapp.NewWithID("io.1loop.desktop")
	a.Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ctx, cancel := context.WithCancel(context.Background())
	ui := &desktopUI{backend: backend, ctx: ctx, shutdown: cancel, keys: make(map[string]string), statePath: statePath(root),
		cfg: config{Provider: "vllm", BaseURL: defaultVLLMURL, Workspace: root}}
	ui.load()
	ui.initializeConnections()
	// Our own title bar on X11 and native Wayland; the system bar elsewhere.
	ui.chrome = customChromeSupported() && os.Getenv("ONELOOP_SYSTEM_TITLEBAR") == ""
	if drv, ok := a.Driver().(desktop.Driver); ok && ui.chrome {
		ui.window = drv.CreateSplashWindow()
		ui.window.SetTitle("1loop")
		ui.window.SetMaster()
	} else {
		ui.chrome = false
		ui.window = a.NewWindow("1loop")
	}
	ui.window.SetPadded(false)
	ui.window.Resize(fyne.NewSize(1180, 760))
	ui.window.CenterOnScreen()
	ui.window.SetIcon(brandResource("1loop-anneau-fond-noir.png"))
	ui.window.SetContent(ui.build())
	ui.window.SetCloseIntercept(ui.quit)
	for _, s := range []*desktop.CustomShortcut{
		{KeyName: fyne.KeyK, Modifier: fyne.KeyModifierControl},
		{KeyName: fyne.KeyR, Modifier: fyne.KeyModifierControl | fyne.KeyModifierShift},
		{KeyName: fyne.KeyB, Modifier: fyne.KeyModifierControl},
	} {
		ui.window.Canvas().AddShortcut(s, func(s fyne.Shortcut) { ui.shortcut(s) })
	}
	ui.window.Canvas().AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) { ui.copySelection() })
	ui.updateHeader()
	ui.renderChats()
	ui.refreshModels()
	ui.refreshContextRing()
	ui.startRemote()
	ui.window.Canvas().Focus(ui.prompt)
	// The launch sequence covers the first frames and only starts once the
	// window is really on screen, otherwise it would play unseen.
	launch := newLaunchScreen(ui.window.Canvas(), func() { ui.window.Canvas().Focus(ui.prompt) })
	launch.show()
	a.Lifecycle().SetOnStarted(func() {
		if ui.chrome {
			chromeReady()
		}
		windowIdentity(ui.window) // again, should the window not have existed yet below
		ui.startFrames()
		size := ui.window.Canvas().Size()
		devf("app", "window %.0f x %.0f at scale %.2f, own title bar %v", size.Width, size.Height, ui.window.Canvas().Scale(), ui.chrome)
		time.AfterFunc(180*time.Millisecond, func() { fyne.Do(launch.play) })
	})
	// Shown, named, then run: the desktop learns which application this is
	// before the window's first frame, when it picks its name and icon.
	ui.window.Show()
	if kind := os.Getenv("ONELOOP_BENCH"); kind != "" && dev != nil {
		go ui.bench(kind)
	}
	windowIdentity(ui.window)
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

func (ui *desktopUI) quit() {
	ui.release()
	ui.window.SetCloseIntercept(nil)
	ui.window.Close()
}

// restart (Ctrl+Shift+R) reloads everything: state is saved, the running
// request is stopped, then the process replaces itself with a fresh copy,
// so the interface, the engine and the launch animation all start again.
func (ui *desktopUI) restart() {
	exe, err := os.Executable()
	if err != nil {
		ui.notice("Restart", err.Error())
		return
	}
	devf("ui", "restart (Ctrl+Shift+R)")
	ui.release()
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

func (ui *desktopUI) shortcut(s fyne.Shortcut) bool {
	if _, copies := s.(*fyne.ShortcutCopy); copies {
		// Ctrl+C with the prompt focused but nothing selected in it: the
		// text selected in the conversation is what is meant.
		return (ui.prompt == nil || ui.prompt.SelectedText() == "") && ui.copySelection()
	}
	c, ok := s.(*desktop.CustomShortcut)
	if !ok {
		return false
	}
	switch {
	case c.KeyName == fyne.KeyK && c.Modifier == fyne.KeyModifierControl:
		ui.startNew()
	case c.KeyName == fyne.KeyR && c.Modifier == fyne.KeyModifierControl|fyne.KeyModifierShift:
		ui.restart()
	case c.KeyName == fyne.KeyB && c.Modifier == fyne.KeyModifierControl:
		ui.toggleSidebar()
	default:
		return false
	}
	return true
}

// release stops background work and saves, before closing or restarting.
func (ui *desktopUI) release() {
	ui.shutdown() // every run's context derives from it
	ui.panel.dispose()
	ui.remote.close()
	ui.mark.stop()
	ui.workMark.stop()
	ui.persist(true)
	ui.browser.waitClosed() // Chrome writes its profile as it closes
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
	label := &fitLabel{text: text, size: size, color: c, label: textLine(text, size, c)}
	label.ExtendBaseWidget(label)
	return label
}

func (label *fitLabel) SetText(text string) {
	if text == label.text {
		return
	}
	label.text = text
	label.Refresh()
}

func (label *fitLabel) CreateRenderer() fyne.WidgetRenderer { return &fitLabelRenderer{label} }

type fitLabelRenderer struct{ fitLabel *fitLabel }

func (renderer *fitLabelRenderer) Layout(size fyne.Size) {
	renderer.fitLabel.label.Text = fitText(renderer.fitLabel.text, renderer.fitLabel.size, fyne.TextStyle{}, size.Width)
	renderer.fitLabel.label.Move(fyne.NewPos(0, (size.Height-renderer.fitLabel.label.MinSize().Height)/2))
}
func (renderer *fitLabelRenderer) MinSize() fyne.Size {
	return fyne.NewSize(24, textSize("Ag", renderer.fitLabel.size, fyne.TextStyle{}).Height)
}
func (renderer *fitLabelRenderer) Refresh() {
	renderer.fitLabel.label.Color = renderer.fitLabel.color
	renderer.Layout(renderer.fitLabel.Size())
	renderer.fitLabel.label.Refresh()
}
func (renderer *fitLabelRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.fitLabel.label}
}
func (renderer *fitLabelRenderer) Destroy() {}

// flatScrollTheme drops the scroll edge shadows: the conversation fades
// under the composer instead of ending on a hard line.
type flatScrollTheme struct{ fyne.Theme }

func (themeSpec flatScrollTheme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameShadow {
		return color.Transparent
	}
	return themeSpec.Theme.Color(name, v)
}

func (ui *desktopUI) build() fyne.CanvasObject {
	ui.tips = newTipLayer()
	ui.buildSettings()
	sidebar := ui.buildSidebar()

	// Conversation column. While the model works, the animated ring sits
	// under the last message.
	afterTypeset = func() {
		if ui.ctx.Err() == nil {
			ui.relayoutColumn()
		}
	}
	ui.items = &transcriptLayout{ui: ui}
	ui.items.reset()
	ui.transcript = container.New(ui.items)
	ui.views, ui.copyRows = map[int]any{}, map[int]*fyne.Container{}
	ui.workMark = newLoopMark(inlineMarkSize)
	ui.thinkLabel = newFitLabel("", 14, colMuted)
	ui.thinking = container.NewBorder(nil, nil, container.NewCenter(ui.workMark.image), nil, padded(0, 0, 12, 0, ui.thinkLabel))
	ui.inlineSlot = container.NewStack(ui.thinking)
	ui.inlineRow = padded(6, 0, 4, 0, ui.inlineSlot)
	ui.inlineRow.Hide()
	ui.dockRow = container.NewStack()
	ui.dockRow.Hide()
	ui.column = container.NewVBox(ui.transcript, ui.inlineRow)
	ui.scroll = newKineticVScroll(container.New(columnLayout{width: 760, inset: 24}, ui.column))
	ui.followTail = true
	ui.scroll.OnScrolled = func(position fyne.Position) {
		// A small tolerance absorbs fractional pixel rounding at the bottom.
		// Content.Size, not MinSize: MinSize walks the whole chat on every event.
		bottom := fyne.Max(0, ui.scroll.Content.Size().Height-ui.scroll.Size().Height)
		ui.followTail = bottom-position.Y <= 2
		relayout(ui.transcript) // items entering the view come back, those leaving it rest
	}
	ui.mark = newLoopMark(60)
	ui.welcome = container.NewCenter(container.NewVBox(
		container.NewCenter(ui.mark.image), spacer(0, 18),
		container.NewCenter(textLine("What shall we build today?", 24, colText)), spacer(0, 4),
		container.NewCenter(textLine("Your project. Your model. One loop.", 14, colMuted)), spacer(0, 40)))
	// Nothing is drawn between the conversation and the composer: no rule,
	// no fade (a gradient of the background over itself showed as darker
	// bands, 8 bits being too few for so dark a colour). The messages just
	// end where the composer's block begins.
	scroll := container.NewThemeOverride(ui.scroll, flatScrollTheme{loopTheme{theme.DefaultTheme()}})
	view := container.NewStack(ui.welcome, scroll)
	ui.scroll.Hide()

	conversation := container.NewBorder(nil, ui.buildFooter(), nil, nil, view)
	// The activity line may sit beside the composer (dockActivity): the
	// dock layer floats over the conversation's bottom left corner.
	conversation = container.New(dockLayout{ui}, conversation, ui.dockRow)
	ui.panel = newToolPanel(ui)
	ui.split = container.New(splitLayout{ui.panel}, conversation, ui.panel.view, newPanelResizeHandle(ui.panel))

	// Title bar: conversation title, drag region and window controls.
	ui.header = newFitLabel("New chat", 14, colMuted)
	ui.expand = newRow("", iconSidebar, styleWindow, ui.toggleSidebar)
	ui.expand.Hide()
	ui.panelToggle = newRow("", iconPanel, styleWindow, func() {
		if ui.panel.target > 0 {
			ui.panel.fold()
		} else {
			ui.panel.unfold()
		}
	})
	ui.panelToggle.onHover = func(in bool) {
		if in {
			ui.tips.show(ui.panelToggle, "Toggle side panel")
		} else {
			ui.tips.hide()
		}
	}
	right := container.New(layout.NewCustomPaddedHBoxLayout(2), container.NewCenter(ui.panelToggle))
	if ui.chrome {
		right.Add(windowControls(ui.window, ui.quit))
	}
	ui.headerBar = container.NewBorder(nil, nil, padded(0, 0, 0, 8, container.NewCenter(ui.expand)), right, ui.header)
	var header fyne.CanvasObject = padded(14, 8, 24, 24, ui.headerBar)
	if ui.chrome {
		header = container.NewStack(newDragArea(ui.window), padded(8, 4, 20, 8, ui.headerBar))
	}
	// The settings page takes the conversation's place while it is open.
	main := container.NewStack(canvas.NewRectangle(colBackground), container.NewBorder(header, nil, nil, nil, container.NewStack(ui.split, ui.prefs.view)))
	// No rule between the panes: the sidebar only casts a faint shadow.
	shade := canvas.NewHorizontalGradient(color.NRGBA{A: 0x2a}, color.NRGBA{})
	ui.pane = &paneLayout{shown: 1, shade: 14}
	ui.root = container.New(ui.pane, sidebar, main, shade)
	layers := []fyne.CanvasObject{ui.root}
	if ui.chrome {
		layers = append(layers, resizeEdges(ui.window))
	}
	return container.NewStack(append(layers, ui.tips.layer)...)
}

// buildFooter assembles the context line, the queue tray and the composer
// card: the card's own outline is the only edge of the input area.
func (ui *desktopUI) buildFooter() fyne.CanvasObject {
	ui.prompt = newChatEntry()
	ui.prompt.SetPlaceHolder("Describe what you want to build…")
	ui.prompt.send = ui.submit
	ui.prompt.steer = ui.steerNow
	ui.prompt.escape = func() { ui.finishEdit(false); ui.clearSelection() }
	ui.window.Canvas().SetOnTypedKey(func(key *fyne.KeyEvent) {
		if key.Name == fyne.KeyEscape && ui.prefs.open { // Escape goes back a page, then to the conversation
			ui.prefs.back()
		}
	})
	ui.prompt.shortcut = ui.shortcut
	ui.prompt.paste = ui.pasteFromClipboard
	ui.prompt.resized = func() {
		if ui.split != nil {
			relayout(ui.split)
		}
	}

	ui.model = newPicker(ui.window, "Loading models…", styleChip, func(label string) {
		if choice, ok := ui.choices[label]; ok {
			ui.selectModel(choice)
		}
	})
	ui.model.row.onTap = ui.modelMenu
	ui.send = newIconButton(iconSend, colAmber, colInk, ui.submit)
	ui.send.iconPx = 20
	ui.stop = newIconButton(iconStop, colText, colInk, func() {
		if ui.current != nil && ui.current.run != nil {
			devf("ui", "stop pressed for %q", ui.current.Title)
			ui.current.run.cancel()
		}
	})
	ui.stop.iconPx = 20
	ui.stop.Hide()
	ui.approvalControl = newRow("", iconShield, styleChip, ui.approvalMenu)
	ui.updateApprovalControl()
	ui.contextRing = newContextRing(ui.tips)
	ui.attach = newIconButton(iconPlus, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0d}, color.NRGBA{}, nil)
	ui.attach.iconPx, ui.attach.padPx = 18, 6
	ui.attach.onTap = func() {
		showMenu(ui.window, ui.attach, []menuAction{
			{label: "Paste image", detail: "From the clipboard", icon: iconCopy, run: func() {
				if !ui.pasteFromClipboard() {
					ui.notice("Paste", "No image in the clipboard.")
				}
			}},
			{label: "Attach files…", detail: "Copy to the workspace tmp folder", icon: iconFolder, run: ui.pickFiles},
			{label: "Attach images…", detail: "Choose files on this computer", icon: iconFolder, run: ui.pickImages},
		})
	}

	actions := container.New(layout.NewCustomPaddedHBoxLayout(4), ui.contextRing, ui.model.row, container.NewStack(ui.send, ui.stop))
	ui.controls = container.New(&composerControlsLayout{}, container.NewCenter(ui.approvalControl), actions)
	ui.model.relayout = ui.controls.Refresh

	card := canvas.NewRectangle(colSurface)
	card.CornerRadius = 16
	card.StrokeColor = colBorder
	card.StrokeWidth = 1
	ui.prompt.onFocus = func(focused bool) {
		from, to := colBorder, colBorderHot
		if !focused {
			from, to = to, from
		}
		canvas.NewColorRGBAAnimation(from, to, 160*time.Millisecond, func(c color.Color) {
			card.StrokeColor = c
			card.Refresh()
		}).Start()
	}
	ui.attachRow = container.New(layout.NewCustomPaddedHBoxLayout(8))
	// The optional rows are hidden with their margins: a hidden child inside
	// a visible padded box would still take its padding and the gap after it.
	ui.attachTray = padded(2, 4, 2, 0, container.NewHScroll(ui.attachRow))
	ui.attachTray.Hide()
	input := container.NewThemeOverride(ui.prompt, bareInputTheme{loopTheme{theme.DefaultTheme()}})
	line := container.New(attachButtonLayout{}, input, ui.attach)
	pencil := canvas.NewImageFromResource(tinted(iconPencil, colAmber))
	pencil.SetMinSize(fyne.NewSquareSize(14))
	cancelEdit := newRow("Cancel", nil, styleChip, func() { ui.finishEdit(false) })
	cancelEdit.size = 12.5
	ui.editBar = padded(0, 0, 4, 0, container.NewBorder(nil, nil,
		container.NewHBox(container.NewCenter(pencil), textLine("Editing a queued message", 12.5, colAmber)),
		container.NewHBox(container.NewCenter(textLine("Enter to save · Esc to cancel", 12, colFaint)), cancelEdit)))
	ui.editBar.Hide()
	composer := container.NewStack(card, padded(8, 8, 10, 10, container.NewVBox(ui.editBar, ui.attachTray, line, ui.controls)))
	ui.composer = composer

	// Messages waiting for the running task sit in a tray on top of the card.
	ui.queueTitle = newFitLabel("", 12.5, colMuted)
	clear := newRow("Clear", nil, styleChip, ui.clearQueue)
	clear.size = 12.5
	ui.queueList = container.New(layout.NewCustomPaddedVBoxLayout(0))
	trayBg := canvas.NewRectangle(colPopover)
	trayBg.TopLeftCornerRadius, trayBg.TopRightCornerRadius = 12, 12
	trayBg.StrokeColor = colBorderSoft
	trayBg.StrokeWidth = 1
	trayBody := container.NewVBox(container.NewBorder(nil, nil, nil, clear, padded(0, 0, 8, 0, ui.queueTitle)), ui.queueList)
	ui.queueTray = padded(0, 0, 16, 16, container.NewStack(trayBg, padded(4, 6, 4, 4, trayBody)))
	ui.queueTray.Hide()

	ui.folder = newRow(filepath.Base(ui.cfg.Workspace), theme.FolderIcon(), styleChip, nil)
	ui.folder.onTap = func() { ui.projectMenu(ui.folder) }
	machine := newRow("This computer", theme.ComputerIcon(), styleChip, nil)
	contextRow := container.NewHBox(ui.folder, machine)
	stack := container.New(layout.NewCustomPaddedVBoxLayout(0), padded(0, 6, 4, 4, contextRow), ui.queueTray, composer)
	return padded(0, 0, 24, 24, container.New(columnLayout{width: 760, inset: 0}, padded(0, 18, 0, 0, stack)))
}

func (ui *desktopUI) updateHeader() {
	ui.folder.SetText(filepath.Base(ui.cfg.Workspace))
	title := "New chat"
	if ui.current != nil {
		title = ui.current.Title
	}
	if ui.prefs != nil && ui.prefs.open {
		title = "Settings"
	}
	ui.header.SetText(title)
}

func (ui *desktopUI) startNew() {
	devf("ui", "new chat screen (workspace %s)", ui.cfg.Workspace)
	ui.prefs.close()
	ui.clearSelection()
	ui.finishEdit(false)
	ui.current = nil
	ui.cfg.Session = ""
	ui.followTail = true
	ui.transcript.RemoveAll()
	ui.items.reset()
	ui.views, ui.copyRows = map[int]any{}, map[int]*fyne.Container{}
	ui.panel.switchTo(nil)
	ui.showTranscript(false)
	ui.syncRun()
	ui.mark.intro()
	ui.updateHeader()
	ui.renderChats()
	ui.refreshContextRing()
	ui.window.Canvas().Focus(ui.prompt)
}

// showTranscript swaps the welcome screen and the conversation.
func (ui *desktopUI) showTranscript(show bool) {
	if show {
		ui.welcome.Hide()
		ui.scroll.Show()
	} else {
		ui.scroll.Hide()
		ui.welcome.Show()
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
	line := textSize("Ag", theme.TextSize(), fyne.TextStyle{Monospace: true}).Height + theme.LineSpacing()
	scroll.SetMinSize(fyne.NewSize(0, fyne.Min(maxHeight, fyne.Max(48, lines*line+16))))
	return surface(scroll, colCode, colBorderSoft, 10, 4)
}

// modal opens a themed sheet with a title, a body and right-aligned actions.
// With no actions it shows a single "Close" button.
func (ui *desktopUI) modal(title string, body fyne.CanvasObject, width float32, onDismiss func(), actions ...*tapRow) *sheet {
	var dialog *sheet
	closeButton := newRow("", iconClose, styleWindow, func() { dialog.dismiss() })
	heading := textLine(title, 16, colText)
	heading.TextStyle = fyne.TextStyle{Bold: true}
	if len(actions) == 0 {
		actions = []*tapRow{newRow("Close", nil, styleOutline, func() { dialog.dismiss() })}
	}
	buttons := container.New(layout.NewCustomPaddedHBoxLayout(8), layout.NewSpacer())
	for _, action := range actions {
		buttons.Add(action)
	}
	content := container.NewBorder(
		padded(0, 12, 0, 0, container.NewBorder(nil, nil, nil, closeButton, container.NewHBox(heading))),
		padded(16, 0, 0, 0, buttons), nil, nil, body)
	dialog = newSheet(ui.window.Canvas(), content, width)
	dialog.onDismiss = onDismiss
	dialog.Show()
	ui.window.Canvas().Focus(dialog)
	return dialog
}

func (ui *desktopUI) notice(title, text string) {
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	ui.modal(title, label, 420, nil)
}

// syncRun shows the open chat's run state: the stop button, the activity
// line under the conversation and the waiting messages.
func (ui *desktopUI) syncRun() {
	if ui.settled != nil {
		ui.settled.Stop()
		ui.settled = nil
	}
	var currentTurn *turn
	if ui.current != nil && ui.current.run != nil {
		currentTurn = ui.current.run.turn
	}
	if currentTurn != nil {
		ui.mark.stop()
		ui.send.Hide()
		ui.stop.Show()
		ui.showActivityLine(true)
		ui.applyActivity(currentTurn)
	} else {
		ui.send.Show()
		ui.stop.Hide()
		ui.showActivityLine(false)
		ui.workMark.setState("brand")
	}
	ui.controls.Refresh()
	ui.renderQueue()
	ui.syncCopies()
	ui.relayoutColumn()
}

// runStarted marks a chat as working.
func (ui *desktopUI) runStarted(chat *conversation, run *chatRun) {
	devf("run", "start %q [%s]: %s %s, effort %q, approval %q, session %q, on screen %v",
		chat.Title, chat.ID, chat.Config.Provider, chat.Config.Model, chat.Config.Effort, approvalFor(ui.approvalMode, chat.Config), chat.Config.Session, chat == ui.current)
	run.started = time.Now()
	chat.run = run
	ui.remote.metaChanged(chat)
	if chat == ui.current {
		ui.syncRun()
	}
	ui.renderChats()
}

// runEnded closes a chat's run. ended is the ring shown briefly under the
// answer when the chat is on screen: "done" or "stopped".
func (ui *desktopUI) runEnded(chat *conversation, ended string) {
	run := chat.run
	if run != nil {
		devf("run", "end %q [%s]: %s after %s, on screen %v", chat.Title, chat.ID, ended, time.Since(run.started).Round(time.Millisecond), chat == ui.current)
	}
	chat.run = nil
	ui.remote.metaChanged(chat)
	if run != nil && run.approval != nil {
		run.approval.dismiss()
	}
	ui.renderChats()
	ui.save()
	if chat != ui.current {
		return
	}
	ui.syncRun()
	ui.refreshContextRing()
	// The closed ring stays briefly under the answer, then goes away.
	ui.showActivityLine(true)
	ui.workMark.setState(ended)
	ui.thinkLabel.SetText(map[string]string{"done": "Done", "stopped": "Stopped"}[ended])
	ui.relayoutColumn()
	ui.settled = time.AfterFunc(1600*time.Millisecond, func() {
		fyne.Do(func() {
			if chat == ui.current && chat.run == nil {
				ui.showActivityLine(false)
				ui.workMark.setState("brand")
				ui.relayoutColumn()
			}
		})
	})
	if ui.window.Canvas().Overlays().Top() == nil { // never pull focus out of an open dialog
		ui.window.Canvas().Focus(ui.prompt)
	}
}

// promptMessage takes what the composer holds as a message.
func (ui *desktopUI) promptMessage() (message, bool) {
	if problem := ui.attachmentProblem(); problem != "" {
		ui.notice("Attachments", problem)
		return message{}, false
	}
	text := strings.TrimSpace(ui.prompt.Text)
	if text == "" && len(ui.attachments) == 0 && len(ui.fileAttachments) == 0 {
		return message{}, false
	}
	messageValue := message{Role: "user", Content: attachmentText(text, ui.fileAttachments), Images: ui.takeAttachments()}
	ui.fileAttachments = nil
	ui.renderAttachments()
	ui.prompt.SetText("")
	return messageValue, true
}

// submit is Enter: send, or queue the message behind the running task.
func (ui *desktopUI) submit() {
	if ui.editing != nil {
		ui.finishEdit(true)
		return
	}
	text := strings.TrimSpace(ui.prompt.Text)
	chat := ui.current
	if chat != nil && chat.run != nil {
		if message, ok := ui.promptMessage(); ok {
			chat.queue = append(chat.queue, message)
			ui.renderQueue()
		}
		return
	}
	if text == "" && len(ui.attachments) == 0 && len(ui.fileAttachments) == 0 {
		// An empty Enter resumes a queue left behind by a stopped task.
		if chat != nil {
			if next, ok := ui.takeNext(chat); ok {
				ui.start(next)
			}
		}
		return
	}
	// Claude Code compacts its own session: there /compact is sent as it is.
	if ui.cfg.Provider != "claudecode" && (text == "/compact" || strings.HasPrefix(text, "/compact ")) {
		ui.prompt.SetText("")
		ui.compactChat(strings.TrimSpace(strings.TrimPrefix(text, "/compact")))
		return
	}
	if !ui.ready() {
		return
	}
	if messageValue, ok := ui.promptMessage(); ok {
		ui.start(messageValue)
	}
}

// steerNow is Ctrl+Enter: the message joins the running task at its next
// step, without waiting for the answer to end.
func (ui *desktopUI) steerNow() {
	chat := ui.current
	if ui.editing != nil || chat == nil || chat.run == nil {
		ui.submit()
		return
	}
	if message, ok := ui.promptMessage(); ok {
		chat.steer = append(chat.steer, message)
		ui.renderQueue()
	}
}

// ready checks that a message can be sent now, and says why not.
func (ui *desktopUI) ready() bool {
	if problem := ui.notReady(); problem != "" {
		ui.notice("Not ready", problem)
		return false
	}
	return true
}

// notReady is why a message cannot be sent now ("" when it can).
func (ui *desktopUI) notReady() string {
	if ui.modelsLoading {
		return "The models are still loading."
	}
	if ui.cfg.Model == "" {
		return "Choose a connection and a model."
	}
	root, err := resolveWorkspace(ui.cfg.Workspace)
	if err != nil {
		return err.Error()
	}
	ui.cfg.Workspace = root
	return ""
}

// start sends a message in the open chat (a new one if none is open) and
// runs the agent loop for it in the background.
func (ui *desktopUI) start(messageValue message) { ui.startWithID(messageValue, "") }

func (ui *desktopUI) startWithID(messageValue message, id string) {
	if !ui.ready() {
		if ui.current != nil { // keep it rather than lose it
			ui.current.queue = append([]message{messageValue}, ui.current.queue...)
			ui.renderQueue()
		}
		return
	}
	chat := ui.current
	if chat == nil {
		title := []rune(strings.Join(strings.Fields(messageValue.Content), " "))
		if len(title) > 36 {
			title = title[:36]
		}
		if len(title) == 0 {
			title = []rune("[image]")
		}
		ui.cfg.Session = "" // a new chat starts its own Claude Code session
		chat = &conversation{ID: identifier()[:16], Title: string(title), Config: ui.cfg}
		ui.chats = append([]*conversation{chat}, ui.chats...)
		ui.current = chat
		ui.panel.adopt(chat) // what was open on the start screen is now this chat's
	}
	ui.panel.todoChanged(chat)
	chat.History = append(chat.History, messageValue)
	ui.showTranscript(true)
	ui.followTail = true // Sending a message explicitly returns to the live answer.
	ui.addItem(chat, chatItem{ID: id, Kind: "user", Text: messageValue.Content, Images: messageValue.Images})
	ui.updateHeader()
	// No deadline: a long task (long shell runs, many tool rounds) must not
	// die on a timer. Only the Stop button or a dead network ends it.
	ctx, cancel := context.WithCancel(ui.ctx)
	cfg := ui.runConfig(chat)
	history := append([]message(nil), chat.History...)
	turn := ui.newTurn(chat, cfg)
	turn.show(activity{State: "wait"})
	ui.runStarted(chat, &chatRun{cancel: cancel, turn: turn})
	pump := &eventPump{handle: turn.handle}
	inboxValue := inbox{
		steer: func() (out []message) {
			ui.onUI(ctx, func() { out = ui.takeSteer(chat) })
			return out
		},
		next: func() (m message, ok bool) {
			ui.onUI(ctx, func() { m, ok = ui.takeNext(chat) })
			return m, ok
		},
	}
	go func() {
		defer cancel()
		err := ui.backend.run(ctx, cfg, history, inboxValue, pump.emit)
		canceled := ctx.Err() == context.Canceled
		fyne.Do(func() {
			pump.flush() // whatever the stream sent last comes first
			if ui.ctx.Err() != nil {
				return
			}
			turn.finish()
			ended := "done"
			if err != nil {
				ended = "stopped"
				text := err.Error()
				if canceled {
					text = "Response stopped."
				}
				ui.addItem(chat, chatItem{Kind: "error", Text: text})
			}
			ui.runEnded(chat, ended)
		})
	}()
}

// runConfig is the chat's configuration with this session's secrets and
// preferences, for one run.
func (ui *desktopUI) runConfig(chat *conversation) config {
	chat.identifyMessages()
	if chat.Todo == nil {
		chat.Todo = &todoState{}
	}
	cfg := chat.Config
	cfg.Todo = chat.Todo
	cfg.Browser = ui.browserTools(chat)
	cfg.Terminal = ui.terminalTools(chat)
	cfg.Computer = ui.computerTools(chat)
	if chat.Cache == nil {
		chat.Cache = &contextCache{}
	}
	cfg.Cache = chat.Cache
	cfg.ContextKey = chat.ID + ":" + chat.Version
	cfg.Recall = func(ctx context.Context) ([]chatItem, error) {
		var items []chatItem
		ui.onUI(ctx, func() { items = append([]chatItem{}, chat.Items...) })
		return items, ctx.Err()
	}
	cfg.APIKey = ui.keys[cfg.ConnectionID]
	cfg.UserInstructions = ui.userInstructions
	cfg.ApprovalMode = approvalFor(ui.approvalMode, cfg)
	cfg.ClassicCompaction = ui.classicCompaction
	return cfg
}

// onUI runs f on the UI thread and waits, unless the run is over.
func (ui *desktopUI) onUI(ctx context.Context, f func()) {
	if ctx.Err() == nil { // closing: never block the loop on the window
		fyne.DoAndWait(f)
	}
}

// compactChat runs the manual /compact command, optionally steered by a
// focus ("/compact keep the API design").
func (ui *desktopUI) compactChat(focus string) {
	chat := ui.current
	if chat == nil || len(chat.History) == 0 {
		ui.notice("Compact", "Nothing to compact yet.")
		return
	}
	if !ui.ready() {
		return
	}
	ctx, cancel := context.WithCancel(ui.ctx)
	cfg := ui.runConfig(chat)
	history := append([]message(nil), chat.History...)
	turn := ui.newTurn(chat, cfg)
	turn.show(activity{State: "compact"})
	ui.runStarted(chat, &chatRun{cancel: cancel, turn: turn})
	pump := &eventPump{handle: turn.handle}
	go func() {
		defer cancel()
		kept, err := ui.backend.compactNow(ctx, cfg, history, focus, pump.emit)
		fyne.Do(func() {
			pump.flush()
			if ui.ctx.Err() != nil {
				return
			}
			ended := "done"
			if err != nil {
				ended = "stopped"
				ui.addItem(chat, chatItem{Kind: "error", Text: err.Error()})
			} else {
				chat.History = kept
				ui.addItem(chat, chatItem{Kind: "note", Text: "Context compacted: the earlier rounds were condensed"})
			}
			ui.runEnded(chat, ended)
		})
	}()
}

// showApproval asks before a command or an edit, whichever chat it comes
// from: the run waits for the answer.
func (ui *desktopUI) showApproval(chat *conversation, p map[string]any) {
	id, _ := p["id"].(string)
	title, _ := p["title"].(string)
	preview, _ := p["preview"].(string)
	if chat != ui.current {
		title += " · " + chat.Title
	}
	resolved := false
	var dialog *sheet
	forget := func() {
		if chat.run != nil && chat.run.approval == dialog {
			chat.run.approval = nil
			chat.run.approvalID, chat.run.answer = "", nil
			ui.remote.metaChanged(chat)
		}
	}
	answer := func(allow bool) {
		if resolved {
			return
		}
		resolved = true
		ui.backend.approve(id, allow)
		forget()
		dialog.Hide()
	}
	refuse := newRow("Deny", nil, styleOutline, func() { answer(false) })
	allow := newRow("Allow", nil, stylePrimary, func() { answer(true) })
	intro := textLine("1loop needs your approval to perform this action.", 14, colMuted)
	body := container.NewBorder(padded(0, 10, 0, 0, intro), nil, nil, nil, codeBlock(preview, 360))
	dialog = ui.modal(title, body, 680, func() {
		if !resolved {
			resolved = true
			ui.backend.approve(id, false)
			forget()
		}
	}, refuse, allow)
	if chat.run != nil {
		chat.run.approval = dialog
		chat.run.approvalID, chat.run.approvalTitle, chat.run.approvalPreview = id, title, preview
		chat.run.answer = answer
		ui.remote.metaChanged(chat)
	}
}

func (ui *desktopUI) refreshModels() {
	ui.modelLoad++
	generation := ui.modelLoad
	previous := ui.cfg
	ui.modelsLoading = true
	connections := append([]connection(nil), ui.connectionsList...)
	keys := make(map[string]string, len(ui.keys))
	for id, key := range ui.keys {
		keys[id] = key
	}
	ui.model.Disable()
	ui.model.placeholder = "Loading models…"
	ui.model.ClearSelected()
	ui.model.row.SetTrailing("")
	ui.send.Disable()
	go func() {
		type result struct {
			choices []modelChoice
			err     error
		}
		results := make([]result, len(connections))
		var wg sync.WaitGroup
		for i, conn := range connections {
			if !conn.Enabled {
				continue
			}
			wg.Add(1)
			go func(i int, connectionValue connection) {
				defer wg.Done()
				results[i].choices, results[i].err = ui.backend.connectionModels(ui.ctx, connectionValue, keys[connectionValue.ID])
				var timeout net.Error
				if connectionValue.Provider == "openai" && errors.As(results[i].err, &timeout) && timeout.Timeout() && ui.ctx.Err() == nil {
					results[i].choices, results[i].err = ui.backend.connectionModels(ui.ctx, connectionValue, keys[connectionValue.ID])
				}
			}(i, conn)
		}
		wg.Wait()
		fyne.Do(func() {
			if ui.ctx.Err() != nil || generation != ui.modelLoad {
				return
			}
			ui.modelsLoading = false
			ui.choices = map[string]modelChoice{}
			ui.model.Details = map[string]string{}
			ui.modelUnavailable = map[string]string{}
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
					ui.modelUnavailable[connections[i].ID] = problem
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
					if _, exists := ui.choices[label]; exists {
						label += fmt.Sprintf(" · %d", len(options)+1)
					}
					options = append(options, label)
					ui.choices[label] = choice
					ui.model.Details[label] = choice.Connection.Name + " · " + choice.ID
					if choice.Connection.Provider == "claudecode" {
						ui.model.Details[label] = "Runs in Claude Code's own harness · " + choice.ID
					}
					if choice.Connection.ID == previous.ConnectionID && choice.ID == previous.Model {
						selected = label
					}
				}
			}
			ui.model.Options = options
			ui.remote.settingsChanged()
			ui.model.Enable()
			if len(options) == 0 {
				ui.cfg.Model = ""
				ui.model.placeholder = "No model available"
				ui.model.refresh()
				return
			}
			if selected == "" {
				selected = options[0]
			}
			ui.model.SetSelected(selected)
			ui.send.Enable()
			ui.refreshContextRing()
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
func (ui *desktopUI) save() {
	if ui.saveTimer != nil {
		ui.saveTimer.Stop()
	}
	ui.saveTimer = time.AfterFunc(400*time.Millisecond, func() { fyne.Do(ui.saveNow) })
}

func (ui *desktopUI) saveNow() { ui.persist(false) }

// persist writes the state file. The conversations are nearly all of it
// (megabytes once they hold tool outputs and images), so they are encoded
// and written beside the interface, from copies taken here: a save used to
// freeze the window for as long as it took. wait is for closing, when the
// file must be on disk before the process ends.
func (ui *desktopUI) persist(wait bool) {
	started := time.Now()
	if ui.saveTimer != nil {
		ui.saveTimer.Stop()
		ui.saveTimer = nil
	}
	remote := ui.remoteEnabled
	// Everything but the chats is small, and holds maps the settings edit:
	// it is encoded here, on the interface's thread.
	head, err := json.Marshal(savedState{ui.claudeOffered, &remote, ui.devices, ui.cfg, nil, ui.projects, ui.connectionsList, ui.userInstructions, ui.approvalMode, ui.panelWidth, ui.classicCompaction})
	fields := map[string]json.RawMessage{}
	if err == nil {
		err = json.Unmarshal(head, &fields)
	}
	if ui.panel != nil {
		ui.panel.stash() // the open chat's panel, as it is now
	}
	chats := make([]*conversation, len(ui.chats))
	for i, chat := range ui.chats { // messages are never edited in place: copying the lists is enough
		chats[i] = &conversation{ID: chat.ID, Title: chat.Title, Config: chat.Config, Panel: chat.Panel,
			History: append([]message{}, chat.History...), Items: append([]chatItem{}, chat.Items...), Todo: chat.Todo.copy(), Cache: chat.Cache.copy(),
			Version: chat.Version, Versions: copyVersions(chat.Versions)}
	}
	ui.saveCount++
	count, path := ui.saveCount, ui.statePath
	write := func() {
		ui.saveLock.Lock()
		defer ui.saveLock.Unlock()
		if count < ui.saveDone {
			return // a newer state is already on disk
		}
		ui.saveDone = count
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
			if err != nil && err.Error() != ui.saveError && ui.ctx.Err() == nil {
				ui.notice("Unable to save", err.Error()) // once per distinct problem
			}
			ui.saveError = ""
			if err != nil {
				ui.saveError = err.Error()
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

func (ui *desktopUI) load() {
	ui.remoteEnabled = true // the remote is on until the user turns it off
	defer ui.remoteDefaults()
	data, err := os.ReadFile(ui.statePath)
	if err != nil {
		return
	}
	var state savedState
	if json.Unmarshal(data, &state) != nil {
		return
	}
	if state.RemoteEnabled != nil {
		ui.remoteEnabled = *state.RemoteEnabled
	}
	ui.devices = state.Devices
	ui.panelWidth = state.PanelWidth
	ui.claudeOffered = state.ClaudeOffered
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
	ui.cfg = state.Config
	ui.projects = state.Projects
	ui.connectionsList = state.Connections
	ui.userInstructions = state.UserInstructions
	ui.approvalMode = state.ApprovalMode
	ui.classicCompaction = state.ClassicCompaction
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
		ui.chats = append(ui.chats, chat)
		ui.ensureProject(chat.Config.Workspace)
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
type dockLayout struct{ ui *desktopUI }

func (layoutSpec dockLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (layoutSpec dockLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.Position{})
	objects[0].Resize(size)
	// The footer centres a 760 wide column between 24 px margins (buildFooter).
	column := fyne.Min(760, size.Width-48)
	left := 24 + (size.Width-48-column)/2
	width := left - 24 - 24 // a margin on each side of the dock
	card := layoutSpec.ui.composer.Size().Height
	objects[1].Move(fyne.NewPos(24, size.Height-18-card))
	objects[1].Resize(fyne.NewSize(fyne.Max(0, width), card))
	if docked := dockActivity && width >= dockMinWidth; docked != layoutSpec.ui.docked {
		fyne.Do(func() { layoutSpec.ui.setDocked(docked) }) // not while laying out
	}
}

// setDocked moves the activity line between its two places.
func (ui *desktopUI) setDocked(docked bool) {
	if docked == ui.docked {
		return
	}
	ui.docked = docked
	from, to, size := ui.inlineSlot, ui.dockRow, float32(dockMarkSize)
	if !docked {
		from, to, size = ui.dockRow, ui.inlineSlot, inlineMarkSize
	}
	from.Remove(ui.thinking)
	to.Add(ui.thinking)
	ui.workMark.resize(size)
	ui.showActivityLine(ui.lineShown)
}

// showActivityLine shows or hides the activity line wherever it is.
func (ui *desktopUI) showActivityLine(show bool) {
	ui.lineShown = show
	if show && ui.docked {
		ui.dockRow.Show()
	} else {
		ui.dockRow.Hide()
	}
	if show && !ui.docked {
		ui.inlineRow.Show()
	} else {
		ui.inlineRow.Hide()
	}
	ui.relayoutColumn()
}
