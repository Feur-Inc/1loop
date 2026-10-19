package server

// The headless server's panel tools: browser tabs and terminals that run on
// the server, with no window to show them.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/target"
	"github.com/hinshun/vt10x"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/platform"
	"1loop/internal/terminal"
)

type serverTab struct {
	browser  *browser.Page
	terminal *serverTerminal
}

func (tab *serverTab) key() string {
	if tab.browser != nil {
		return "browser-" + tab.browser.Snapshot().ID
	}
	return "terminal-" + tab.terminal.id
}

func (tab *serverTab) close() {
	if tab.browser != nil {
		tab.browser.Close()
	}
	if tab.terminal != nil {
		tab.terminal.close()
	}
}

func (server *headlessServer) detachTabLocked(chat *serverChat, index int) *serverTab {
	tab := chat.tabs[index]
	wasBrowser := tab.browser != nil
	chat.tabs = append(chat.tabs[:index], chat.tabs[index+1:]...)
	if !wasBrowser {
		return tab
	}
	for _, remaining := range chat.tabs {
		if remaining.browser != nil {
			return tab
		}
	}
	// Closing the last browser tab cancels its session. The next tab needs
	// a fresh session, without restoring the tabs the user just closed.
	chat.session = browser.NewBrowserSession(server.engine)
	return tab
}

func (server *headlessServer) ensureTabsLocked(chat *serverChat) {
	if chat.session != nil {
		return
	}
	chat.session = browser.NewBrowserSession(server.engine)
	for _, info := range chat.Browsers {
		server.addBrowserLocked(chat, info, "")
	}
}

func (server *headlessServer) addBrowserLocked(chat *serverChat, info browser.Info, attachTarget target.ID) *browser.Page {
	page := browser.NewBrowserPage(chat.session, info)
	page.AttachTarget = attachTarget
	chat.tabs = append(chat.tabs, &serverTab{browser: page})
	page.OnPopup = func(id target.ID, address string) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if server.ctx.Err() != nil {
			return
		}
		server.addBrowserLocked(chat, browser.Info{URL: address, Width: 1280, Height: 800, Adaptive: true}, id)
		server.scheduleSaveLocked()
	}
	// Cold starts must not hold the server lock or Chromium's event callback.
	go page.Start()
	return page
}

func (server *headlessServer) browserTools(chat *serverChat) agent.PanelToolHandler {
	return func(ctx context.Context, name string, raw json.RawMessage) (agent.PanelToolResult, error) {
		var args agent.BrowserToolArgs
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return agent.PanelToolResult{}, err
		}
		server.mu.Lock()
		server.ensureTabsLocked(chat)
		var page *browser.Page
		infos := []browser.Info{}
		index := -1
		for i, tab := range chat.tabs {
			if tab.browser != nil {
				info := tab.browser.Snapshot()
				infos = append(infos, info)
				if args.TabID == "" || args.TabID == info.ID {
					page = tab.browser
					index = i
				}
			}
		}
		if name == "browser_tabs" {
			switch args.Action {
			case "list":
				server.mu.Unlock()
				data, _ := json.Marshal(map[string]any{"tabs": infos})
				return agent.PanelToolResult{Text: string(data)}, nil
			case "open":
				address, err := browser.URL(args.URL)
				if err != nil {
					server.mu.Unlock()
					return agent.PanelToolResult{}, err
				}
				page = server.addBrowserLocked(chat, browser.Info{URL: address, Adaptive: true}, "")
			case "select":
			case "close":
				if page == nil {
					server.mu.Unlock()
					return agent.PanelToolResult{}, fmt.Errorf("Unknown browser tab")
				}
				closed := server.detachTabLocked(chat, index)
				server.scheduleSaveLocked()
				server.mu.Unlock()
				closed.close()
				return agent.PanelToolResult{Text: "Browser tab closed."}, nil
			default:
				server.mu.Unlock()
				return agent.PanelToolResult{}, fmt.Errorf("Unknown tab action")
			}
		}
		server.scheduleSaveLocked()
		server.mu.Unlock()
		if page == nil {
			return agent.PanelToolResult{}, fmt.Errorf("Open a browser tab with browser_tabs first")
		}
		if err := page.Run(ctx); err != nil {
			return agent.PanelToolResult{}, err
		}
		if name == "browser_tabs" {
			data, _ := json.Marshal(page.Snapshot())
			return agent.PanelToolResult{Text: string(data)}, nil
		}
		return agent.ExecuteBrowserTool(ctx, page, name, args)
	}
}

type serverTerminal struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	id, dir string
	file    terminal.Process
	cancel  context.CancelFunc
	done    chan struct{}
	exit    *int
	output  []byte
	total   int64
	screen  vt10x.Terminal
}

func newServerTerminal(parent context.Context, dir string, host terminal.NativeHost) (*serverTerminal, error) {
	ctx, cancel := context.WithCancel(parent)
	file, err := terminal.NewTerminalProcess(ctx, dir, host)
	if err != nil {
		cancel()
		return nil, err
	}
	terminal := &serverTerminal{
		id:     core.Identifier()[:16],
		dir:    dir,
		file:   file,
		cancel: cancel,
		done:   make(chan struct{}),
		screen: vt10x.New(vt10x.WithSize(100, 30), vt10x.WithWriter(file)),
	}
	go func() {
		buffer := make([]byte, 8192)
		for {
			count, err := file.Read(buffer)
			if count > 0 {
				terminal.screen.Write(buffer[:count])
				terminal.mu.Lock()
				terminal.output = append(terminal.output, buffer[:count]...)
				terminal.total += int64(count)
				if len(terminal.output) > 256<<10 {
					cut := len(terminal.output) - (256 << 10)
					for cut < len(terminal.output) && !utf8.RuneStart(terminal.output[cut]) {
						cut++
					}
					terminal.output = append([]byte{}, terminal.output[cut:]...)
				}
				terminal.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		code := file.Wait()
		terminal.mu.Lock()
		terminal.exit = &code
		terminal.mu.Unlock()
		close(terminal.done)
	}()
	return terminal, nil
}

func (terminal *serverTerminal) close() { terminal.cancel(); terminal.file.Close() }
func (terminal *serverTerminal) write(text string) error {
	terminal.writeMu.Lock()
	defer terminal.writeMu.Unlock()
	_, err := io.WriteString(terminal.file, text)
	return err
}

func (terminal *serverTerminal) read(offset, limit int64) map[string]any {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	first := terminal.total - int64(len(terminal.output))
	offset = max(first, min(offset, terminal.total))
	for offset < terminal.total && !utf8.RuneStart(terminal.output[offset-first]) {
		offset++
	}
	end := min(terminal.total, offset+limit)
	// JSON text must not split a multibyte character between offset pages.
	for end < terminal.total && end > offset && !utf8.RuneStart(terminal.output[end-first]) {
		end--
	}
	if end > offset {
		start := end - 1
		for start > offset && !utf8.RuneStart(terminal.output[start-first]) {
			start--
		}
		if !utf8.FullRune(terminal.output[start-first : end-first]) {
			end = start
		}
	}
	return map[string]any{
		"session_id":     terminal.id,
		"cwd":            terminal.dir,
		"output":         string(terminal.output[offset-first : end-first]),
		"available_from": first,
		"next_offset":    end,
		"total":          terminal.total,
		"exit_code":      terminal.exit,
	}
}

func terminalColor(value vt10x.Color, background bool) color.Color {
	palette := []color.NRGBA{
		{30, 30, 30, 255},
		{225, 95, 95, 255},
		{120, 200, 135, 255},
		{235, 195, 90, 255},
		{105, 155, 235, 255},
		{190, 130, 220, 255},
		{100, 200, 210, 255},
		{210, 210, 210, 255},
		{110, 110, 110, 255},
		{255, 135, 135, 255},
		{155, 235, 165, 255},
		{255, 225, 130, 255},
		{145, 190, 255, 255},
		{220, 170, 250, 255},
		{145, 235, 245, 255},
		{245, 245, 245, 255},
	}
	if value < 16 {
		return palette[int(value)]
	}
	if value == vt10x.DefaultFG {
		return palette[7]
	}
	if value == vt10x.DefaultBG || value == vt10x.DefaultCursor {
		if background {
			return palette[0]
		}
		return palette[7]
	}
	if value < 232 {
		n := int(value) - 16
		level := func(v int) uint8 {
			if v == 0 {
				return 0
			}
			return uint8(55 + 40*v)
		}
		return color.NRGBA{level(n / 36), level(n / 6 % 6), level(n % 6), 255}
	}
	if value < 256 {
		v := uint8(8 + (value-232)*10)
		return color.NRGBA{v, v, v, 255}
	}
	return color.NRGBA{uint8(value >> 16), uint8(value >> 8), uint8(value), 255}
}

func (terminal *serverTerminal) capture() []byte {
	terminal.screen.Lock()
	defer terminal.screen.Unlock()
	cols, rows := terminal.screen.Size()
	img := image.NewRGBA(image.Rect(0, 0, cols*7, rows*14))
	draw.Draw(img, img.Bounds(), image.NewUniform(terminalColor(vt10x.DefaultBG, true)), image.Point{}, draw.Src)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cell := terminal.screen.Cell(x, y)
			rect := image.Rect(x*7, y*14, (x+1)*7, (y+1)*14)
			draw.Draw(img, rect, image.NewUniform(terminalColor(cell.BG, true)), image.Point{}, draw.Src)
			if cell.Char > 0 {
				drawer := font.Drawer{Dst: img, Src: image.NewUniform(terminalColor(cell.FG, false)), Face: basicfont.Face7x13, Dot: fixed.P(x*7, y*14+12)}
				drawer.DrawString(string(cell.Char))
			}
		}
	}
	if terminal.screen.CursorVisible() {
		cursor := terminal.screen.Cursor()
		if cursor.X >= 0 && cursor.X < cols && cursor.Y >= 0 && cursor.Y < rows {
			draw.Draw(img, image.Rect(cursor.X*7, cursor.Y*14+12, (cursor.X+1)*7, (cursor.Y+1)*14), image.NewUniform(terminalColor(vt10x.DefaultCursor, false)), image.Point{}, draw.Src)
		}
	}
	var output bytes.Buffer
	png.Encode(&output, img)
	return output.Bytes()
}

func (server *headlessServer) terminalTools(chat *serverChat) agent.PanelToolHandler {
	return func(ctx context.Context, name string, raw json.RawMessage) (agent.PanelToolResult, error) {
		var args struct {
			Action             string
			SessionID          string `json:"session_id"`
			Cwd, Command, Text string
			Keys               []string
			Offset             int64
			MaxBytes           *int `json:"max_bytes"`
			WaitMS             int  `json:"wait_ms"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return agent.PanelToolResult{}, err
		}
		server.mu.Lock()
		var terminal *serverTerminal
		list := []map[string]string{}
		index := -1
		for i, tab := range chat.tabs {
			if tab.terminal != nil {
				list = append(list, map[string]string{"session_id": tab.terminal.id, "cwd": tab.terminal.dir})
				if args.SessionID == "" || args.SessionID == tab.terminal.id {
					terminal = tab.terminal
					index = i
				}
			}
		}
		if name == "terminal_tabs" {
			switch args.Action {
			case "list":
				server.mu.Unlock()
				data, _ := json.Marshal(map[string]any{"terminals": list})
				return agent.PanelToolResult{Text: string(data)}, nil
			case "open":
				dir, revision := chat.Config.Workspace, server.terminalRevision
				parent := platform.WithWorkspaceMode(server.ctx, server.approvalMode == "yolo")
				host := mobileTerminalHost(server.bridge, ctx)
				server.mu.Unlock()
				var err error
				if args.Cwd != "" {
					dir, err = agent.WorkspacePath(dir, args.Cwd)
				}
				if err == nil {
					err = ctx.Err()
				}
				if err == nil {
					terminal, err = newServerTerminal(parent, dir, host)
				}
				if err != nil {
					return agent.PanelToolResult{}, err
				}
				server.mu.Lock()
				if server.closing || server.ctx.Err() != nil || ctx.Err() != nil || revision != server.terminalRevision {
					server.mu.Unlock()
					terminal.close()
					return agent.PanelToolResult{}, fmt.Errorf("Terminal startup was cancelled or permissions changed")
				}
				chat.tabs = append(chat.tabs, &serverTab{terminal: terminal})
			case "select":
			case "close":
				if terminal == nil {
					server.mu.Unlock()
					return agent.PanelToolResult{}, fmt.Errorf("Unknown terminal")
				}
				server.detachTabLocked(chat, index)
				server.mu.Unlock()
				terminal.close()
				return agent.PanelToolResult{Text: "Terminal closed."}, nil
			default:
				server.mu.Unlock()
				return agent.PanelToolResult{}, fmt.Errorf("Unknown terminal action")
			}
		}
		server.mu.Unlock()
		if terminal == nil {
			return agent.PanelToolResult{}, fmt.Errorf("Open a terminal with terminal_tabs first")
		}
		var err error
		switch name {
		case "terminal_command":
			err = terminal.write(args.Command + "\r")
		case "terminal_write":
			err = terminal.write(args.Text)
		case "terminal_keys":
			for _, name := range args.Keys {
				var key string
				key, err = agent.TerminalKey(name)
				if err != nil {
					break
				}
				err = terminal.write(key)
				if err != nil {
					break
				}
			}
		case "terminal_screen":
			return agent.PanelToolResult{Text: "Terminal screenshot", Images: []core.ImagePart{{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(terminal.capture())}}}, nil
		case "terminal_tabs", "terminal_read":
		default:
			return agent.PanelToolResult{}, fmt.Errorf("Unknown terminal tool")
		}
		if err != nil {
			return agent.PanelToolResult{}, err
		}
		if name == "terminal_read" {
			agent.WaitForOutput(ctx, args.WaitMS, func() bool {
				terminal.mu.Lock()
				defer terminal.mu.Unlock()
				return terminal.total > args.Offset
			}, terminal.done)
		} else if args.WaitMS > 0 {
			timer := time.NewTimer(time.Duration(min(5000, args.WaitMS)) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return agent.PanelToolResult{}, ctx.Err()
			case <-timer.C:
			case <-terminal.done:
			}
		}
		data, _ := json.Marshal(terminal.read(args.Offset, agent.TerminalReadLimit(args.MaxBytes)))
		return agent.PanelToolResult{Text: string(data)}, nil
	}
}

func (terminal *serverTerminal) resize(cols, rows int) error {
	if err := terminal.file.Resize(cols, rows); err != nil {
		return err
	}
	terminal.screen.Resize(cols, rows)
	return nil
}
