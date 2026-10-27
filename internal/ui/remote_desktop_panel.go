package ui

// A headless server's side panel in the desktop client: its terminals and
// browser tabs, streamed from the server.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	fyneterm "github.com/fyne-io/terminal"

	"1loop/internal/browser"
	"1loop/internal/remoteapi"
)

func (remote *remoteDesktop) panelContent(tab remoteapi.Tab) (fyne.CanvasObject, context.CancelFunc, func(bool)) {
	ctx, cancel := context.WithCancel(remote.ctx)
	chat := remote.current
	path := "/api/chats/" + chat + "/panel/" + tab.Key
	switch tab.Kind {
	case "todo":
		remote.todoTasks = container.NewVBox()
		remote.todoFooter = note("")
		remote.todoRows = map[string]*widget.Label{}
		remote.todoSignature = ""
		return container.NewStack(container.NewBorder(nil, remote.todoFooter, nil, nil, container.NewVScroll(remote.todoTasks))), cancel, func(bool) {}
	case "browser":
		screen := newRemoteBrowserScreen(remote, ctx, path)
		address := widget.NewEntry()
		address.SetText(tab.URL)
		address.OnSubmitted = func(text string) { screen.input(map[string]any{"action": "navigate", "text": text}) }
		navigate := func(label, action string) fyne.CanvasObject {
			return newRow(label, nil, styleChip, func() { screen.input(map[string]any{"action": action}) })
		}
		nav := container.NewHBox(navigate("←", "back"), navigate("→", "forward"), navigate("Reload", "reload"))
		viewport := widget.NewEntry()
		viewport.SetText("1280 × 800")
		screen.viewportEntry = viewport
		resize := newRow("Apply", nil, styleChip, func() {
			var width, height int
			fmt.Sscanf(viewport.Text, "%d × %d", &width, &height)
			screen.input(map[string]any{"action": "resize", "width": width, "height": height, "pixelRatio": float64(remote.window.Canvas().Scale())})
		})
		console := container.NewVBox()
		console.Hide()
		expression := widget.NewEntry()
		expression.SetPlaceHolder("JavaScript expression")
		output := widget.NewMultiLineEntry()
		output.Disable()
		output.SetMinRowsVisible(5)
		expression.OnSubmitted = func(text string) {
			go func() {
				var result map[string]any
				err := remote.client.json(ctx, "POST", path+"/input", map[string]string{"action": "evaluate", "text": text}, &result)
				fyne.Do(func() {
					if err != nil {
						output.SetText(err.Error())
					} else {
						data, _ := json.MarshalIndent(result, "", "  ")
						output.SetText(string(data))
					}
				})
			}()
		}
		logs := newRow("Read logs", nil, styleChip, func() {
			go func() {
				var result map[string]any
				err := remote.client.json(ctx, "POST", path+"/input", map[string]string{"action": "logs"}, &result)
				fyne.Do(func() {
					if err != nil {
						output.SetText(err.Error())
					} else {
						data, _ := json.MarshalIndent(result, "", "  ")
						output.SetText(string(data))
					}
				})
			}()
		})
		console.Objects = []fyne.CanvasObject{expression, logs, output}
		toggle := newRow("Console", nil, styleChip, func() {
			if console.Visible() {
				console.Hide()
			} else {
				console.Show()
			}
		})
		adaptive := newRow("Adaptive", nil, styleChip, func() {
			screen.requestedSize = fyne.Size{}
			screen.viewport.Adaptive = true
			screen.adaptViewport()
		})
		sizeRow := container.NewBorder(nil, nil, textLine("Viewport", 12, colMuted), container.NewHBox(adaptive, resize), viewport)
		toolbar := container.NewVBox(container.NewBorder(nil, nil, nav, toggle, address), sizeRow)
		return container.NewBorder(toolbar, console, nil, nil, screen), cancel, func(active bool) { screen.setActive(active, address) }
	case "terminal":
		reader, writer := io.Pipe()
		shell := fyneterm.New()
		shell.Resize(fyne.NewSize(600, 400))
		connection := newRemoteTerminalWriter(remote, ctx, path)
		active := &atomic.Bool{}
		configurations := make(chan fyneterm.Config, 4)
		shell.AddListener(configurations)
		go func() {
			defer shell.RemoveListener(configurations)
			for {
				select {
				case <-ctx.Done():
					return
				case config := <-configurations:
					connection.enqueue(map[string]any{"action": "resize", "width": config.Columns, "height": config.Rows})
				}
			}
		}()
		go func() { shell.RunWithConnection(connection, reader) }()
		go func() {
			defer writer.Close()
			offset := int64(0)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return // the deferred Close: the terminal reads EOF and stops
				case <-ticker.C:
				}
				if !active.Load() {
					continue
				}
				var result struct {
					Output        string `json:"output"`
					NextOffset    int64  `json:"next_offset"`
					AvailableFrom int64  `json:"available_from"`
					ExitCode      *int   `json:"exit_code"`
				}
				if err := remote.client.json(ctx, "GET", fmt.Sprintf("%s?raw=1&offset=%d", path, offset), nil, &result); err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
						continue
					}
				}
				if result.Output != "" {
					if _, err := io.WriteString(writer, result.Output); err != nil {
						return
					}
				}
				offset = result.NextOffset
				if result.ExitCode != nil {
					return
				}
			}
		}()
		// Closing the writer ends the terminal's reads with EOF. A closed
		// reader made it read "closed pipe" again and again, logging each
		// one: the window stopped answering.
		return shell, func() { cancel(); writer.Close() }, active.Store
	default:
		body := container.NewStack(note("Loading…"))
		mode := widget.NewSelect([]string{"Unstaged and new files", "Staged"}, nil)
		mode.SetSelected("Unstaged and new files")
		load := func() {
			staged := mode.Selected == "Staged"
			go func() {
				var result struct{ Diff string }
				endpoint := path
				if staged {
					endpoint += "?staged=1"
				}
				err := remote.client.json(ctx, "GET", endpoint, nil, &result)
				fyne.Do(func() {
					if ctx.Err() != nil {
						return
					}
					if err != nil {
						body.Objects = []fyne.CanvasObject{container.NewVScroll(note(err.Error()))} // a long error must not grow the window
					} else {
						body.Objects = []fyne.CanvasObject{container.NewVScroll(newDiffView(result.Diff))}
					}
					body.Refresh()
				})
			}()
		}
		mode.OnChanged = func(string) { load() }
		load()
		return container.NewBorder(container.NewBorder(nil, nil, mode, newRow("Refresh", nil, styleChip, load), nil), nil, nil, nil, body), cancel, func(bool) {}
	}

}

type remoteTerminalWriter struct {
	remote   *remoteDesktop
	ctx      context.Context
	path     string
	commands chan map[string]any
}

func newRemoteTerminalWriter(remote *remoteDesktop, ctx context.Context, path string) *remoteTerminalWriter {
	writer := &remoteTerminalWriter{remote: remote, ctx: ctx, path: path, commands: make(chan map[string]any, 128)}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case args := <-writer.commands:
				request, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := remote.client.json(request, "POST", path+"/input", args, nil)
				cancel()
				if err != nil && ctx.Err() == nil {
					fyne.Do(func() { remote.status.SetText(err.Error()) })
				}
			}
		}
	}()
	return writer
}

func (writer *remoteTerminalWriter) enqueue(args map[string]any) error {
	select {
	case <-writer.ctx.Done():
		return writer.ctx.Err()
	case writer.commands <- args:
		return nil
	default:
		return fmt.Errorf("Terminal input queue is full")
	}
}

func (writer *remoteTerminalWriter) Write(data []byte) (int, error) {
	if err := writer.enqueue(map[string]any{"action": "write", "text": string(data)}); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (writer *remoteTerminalWriter) Close() error { return nil }

type remoteBrowserScreen struct {
	modifiers     fyne.KeyModifier
	requestedSize fyne.Size
	viewportEntry *widget.Entry
	active        context.CancelFunc
	widget.BaseWidget
	remote   *remoteDesktop
	ctx      context.Context
	path     string
	image    *canvas.Image
	cursor   *canvas.Circle
	viewport browser.PhoneState
	commands chan map[string]any
}

func newRemoteBrowserScreen(remote *remoteDesktop, ctx context.Context, path string) *remoteBrowserScreen {
	screen := &remoteBrowserScreen{
		remote:   remote,
		ctx:      ctx,
		path:     path,
		image:    canvas.NewImageFromImage(nil),
		cursor:   canvas.NewCircle(colAmber),
		commands: make(chan map[string]any, 128),
	}
	screen.image.FillMode = canvas.ImageFillContain
	screen.cursor.Hide()
	screen.ExtendBaseWidget(screen)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case args := <-screen.commands:
				request, cancel := context.WithTimeout(ctx, 15*time.Second)
				err := remote.client.json(request, "POST", path+"/input", args, nil)
				cancel()
				if err != nil && ctx.Err() == nil {
					fyne.Do(func() { remote.status.SetText(err.Error()) })
				}
			}
		}
	}()
	return screen
}

func (screen *remoteBrowserScreen) input(args map[string]any) {
	select {
	case <-screen.ctx.Done():
	case screen.commands <- args:
	default:
		screen.remote.status.SetText("Browser input queue is full; wait for the server.")
	}
}

func (screen *remoteBrowserScreen) setActive(active bool, address *widget.Entry) {
	if screen.active != nil && !active {
		screen.active()
		screen.active = nil
	}
	if active && screen.active == nil {
		ctx, cancel := context.WithCancel(screen.ctx)
		screen.active = cancel
		screen.adaptViewport()
		go screen.stream(ctx, address)
	}
}

func (screen *remoteBrowserScreen) stream(ctx context.Context, address *widget.Entry) {
	for ctx.Err() == nil {
		response, err := screen.remote.client.request(ctx, "GET", screen.path+"/stream?format=png", nil)
		if err == nil {
			for {
				var header [5]byte
				if _, err = io.ReadFull(response.Body, header[:]); err != nil {
					break
				}
				length := binary.BigEndian.Uint32(header[1:])
				if length > 32<<20 {
					break
				}
				payload := make([]byte, int(length))
				if _, err = io.ReadFull(response.Body, payload); err != nil {
					break
				}
				switch header[0] {
				case 'F':
					img, _, err := image.Decode(bytes.NewReader(payload))
					if err == nil {
						fyne.DoAndWait(func() {
							if ctx.Err() == nil {
								screen.image.Image = img
								screen.Refresh()
							}
						})
					}
				case 'S':
					var state browser.PhoneState
					if json.Unmarshal(payload, &state) == nil {
						fyne.DoAndWait(func() {
							if ctx.Err() == nil {
								screen.viewport = state
								if entry := screen.viewportEntry; entry != nil && screen.remote.window.Canvas().Focused() != entry {
									entry.SetText(fmt.Sprintf("%d × %d", state.Width, state.Height))
								}
								screen.adaptViewport()
								if screen.remote.window.Canvas().Focused() != address {
									address.SetText(state.URL)
								}
								screen.Refresh()
							}
						})
					}
				}
			}
			response.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (screen *remoteBrowserScreen) Resize(size fyne.Size) {
	screen.BaseWidget.Resize(size)
	screen.adaptViewport()
}

func (screen *remoteBrowserScreen) adaptViewport() {
	size := screen.Size()
	if !screen.viewport.Adaptive || screen.active == nil || size.Width < 160 || size.Height < 120 || screen.requestedSize == size {
		return
	}
	screen.requestedSize = size
	screen.input(map[string]any{
		"action":     "resize",
		"width":      max(240, min(3840, int(size.Width))),
		"height":     max(200, min(2160, int(size.Height))),
		"adaptive":   true,
		"pixelRatio": float64(screen.remote.window.Canvas().Scale()),
	})
}

func (screen *remoteBrowserScreen) frame() (x, y, width, height float32) {
	size := screen.Size()
	width, height = size.Width, size.Height
	if screen.viewport.Width <= 0 || screen.viewport.Height <= 0 {
		return 0, 0, width, height
	}
	ratio := float32(screen.viewport.Width) / float32(screen.viewport.Height)
	if width/height > ratio {
		width = height * ratio
	} else {
		height = width / ratio
	}
	return (size.Width - width) / 2, (size.Height - height) / 2, width, height
}

func (screen *remoteBrowserScreen) coords(position fyne.Position) (float64, float64, bool) {
	x, y, width, height := screen.frame()
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	px, py := float64((position.X-x)/width), float64((position.Y-y)/height)
	return px, py, px >= 0 && px <= 1 && py >= 0 && py <= 1
}

func (screen *remoteBrowserScreen) Tapped(event *fyne.PointEvent) {
	screen.remote.window.Canvas().Focus(screen)
	x, y, ok := screen.coords(event.Position)
	if ok {
		screen.input(map[string]any{"action": "tap", "x": x, "y": y})
	}
}

func (screen *remoteBrowserScreen) Scrolled(event *fyne.ScrollEvent) {
	x, y, ok := screen.coords(event.Position)
	if ok {
		screen.input(map[string]any{"action": "scroll", "x": x, "y": y, "dy": -float64(event.Scrolled.DY) * .1, "dx": -float64(event.Scrolled.DX) * .1})
	}
}

func (screen *remoteBrowserScreen) TypedRune(value rune) {
	if screen.modifiers&(fyne.KeyModifierControl|fyne.KeyModifierAlt|fyne.KeyModifierSuper) != 0 {
		return
	}
	screen.input(map[string]any{"action": "text", "text": string(value)})
}

func browserRemoteChord(key fyne.KeyName, modifiers fyne.KeyModifier) string {
	parts := []string{}
	for _, modifier := range []struct {
		flag fyne.KeyModifier
		name string
	}{{fyne.KeyModifierControl, "Control"}, {fyne.KeyModifierAlt, "Alt"}, {fyne.KeyModifierShift, "Shift"}, {fyne.KeyModifierSuper, "Meta"}} {
		if modifiers&modifier.flag != 0 {
			parts = append(parts, modifier.name)
		}
	}
	return strings.Join(append(parts, string(key)), "+")
}

func (screen *remoteBrowserScreen) TypedKey(event *fyne.KeyEvent) {
	// Printable characters arrive through TypedRune; forwarding both doubles text.
	if browser.NamedKey(string(event.Name)) != "" {
		screen.input(map[string]any{"action": "key", "text": browserRemoteChord(event.Name, screen.modifiers)})
	}
}

func (screen *remoteBrowserScreen) TypedShortcut(shortcut fyne.Shortcut) {
	switch shortcut.(type) {
	case *fyne.ShortcutPaste:
		screen.input(map[string]any{"action": "text", "text": fyne.CurrentApp().Clipboard().Content()})
	case *fyne.ShortcutSelectAll:
		screen.input(map[string]any{"action": "key", "text": "Control+A"})
	case *fyne.ShortcutCopy, *fyne.ShortcutCut:
		_, cut := shortcut.(*fyne.ShortcutCut)
		go func() {
			var result struct{ Result string }
			err := screen.remote.client.json(screen.ctx, "POST", screen.path+"/input", map[string]string{"action": "evaluate", "text": browserSelectionJS}, &result)
			if err == nil {
				fyne.Do(func() {
					fyne.CurrentApp().Clipboard().SetContent(result.Result)
					if cut {
						screen.input(map[string]any{"action": "evaluate", "text": browserCutJS})
					}
				})
			}
		}()
	}
}

func browserModifier(key fyne.KeyName) fyne.KeyModifier {
	switch key {
	case desktop.KeyControlLeft, desktop.KeyControlRight:
		return fyne.KeyModifierControl
	case desktop.KeyAltLeft, desktop.KeyAltRight:
		return fyne.KeyModifierAlt
	case desktop.KeyShiftLeft, desktop.KeyShiftRight:
		return fyne.KeyModifierShift
	case desktop.KeySuperLeft, desktop.KeySuperRight:
		return fyne.KeyModifierSuper
	}
	return 0
}

func (screen *remoteBrowserScreen) KeyDown(event *fyne.KeyEvent) {
	screen.modifiers |= browserModifier(event.Name)
}

func (screen *remoteBrowserScreen) KeyUp(event *fyne.KeyEvent) {
	screen.modifiers &^= browserModifier(event.Name)
}

func (screen *remoteBrowserScreen) FocusGained()           {}
func (screen *remoteBrowserScreen) FocusLost()             { screen.modifiers = 0 }
func (screen *remoteBrowserScreen) FocusChanged(bool)      {}
func (screen *remoteBrowserScreen) AcceptsTab() bool       { return true }
func (screen *remoteBrowserScreen) Cursor() desktop.Cursor { return desktop.DefaultCursor }
func (screen *remoteBrowserScreen) CreateRenderer() fyne.WidgetRenderer {
	return &remoteBrowserRenderer{screen: screen}
}

type remoteBrowserRenderer struct{ screen *remoteBrowserScreen }

func (renderer *remoteBrowserRenderer) MinSize() fyne.Size { return fyne.NewSize(300, 300) }
func (renderer *remoteBrowserRenderer) Layout(size fyne.Size) {
	screen := renderer.screen
	screen.image.Resize(size)
	x, y, width, height := screen.frame()
	if cursor := screen.viewport.Cursor; cursor != nil && screen.viewport.Width > 0 && screen.viewport.Height > 0 {
		screen.cursor.Resize(fyne.NewSquareSize(9))
		screen.cursor.Move(fyne.NewPos(x+float32(cursor.X)*width/float32(screen.viewport.Width)-4, y+float32(cursor.Y)*height/float32(screen.viewport.Height)-4))
		screen.cursor.Show()
	} else {
		screen.cursor.Hide()
	}
}

func (renderer *remoteBrowserRenderer) Refresh() {
	renderer.Layout(renderer.screen.Size())
	renderer.screen.image.Refresh()
	renderer.screen.cursor.Refresh()
}

func (renderer *remoteBrowserRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.screen.image, renderer.screen.cursor}
}

func (renderer *remoteBrowserRenderer) Destroy() {}
