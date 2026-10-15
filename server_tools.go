//go:build server

package main

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
	"os"
	"os/exec"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/target"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

type serverTab struct {
	browser  *browserPage
	terminal *serverTerminal
}

func (tab *serverTab) key() string {
	if tab.browser != nil {
		return "browser-" + tab.browser.snapshot().ID
	}
	return "terminal-" + tab.terminal.id
}

func (tab *serverTab) close() {
	if tab.browser != nil {
		tab.browser.close()
	}
	if tab.terminal != nil {
		tab.terminal.close()
	}
}

func (server *headlessServer) ensureTabsLocked(chat *serverChat) {
	if chat.session != nil {
		return
	}
	chat.session = newBrowserSession(server.engine)
	for _, info := range chat.Browsers {
		server.addBrowserLocked(chat, info)
	}
}

func (server *headlessServer) addBrowserLocked(chat *serverChat, info browserInfo) *browserPage {
	browser := newBrowserPage(chat.session, info)
	chat.tabs = append(chat.tabs, &serverTab{browser: browser})
	browser.onPopup = func(id target.ID, address string) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if server.ctx.Err() != nil {
			return
		}
		child := server.addBrowserLocked(chat, browserInfo{URL: address, Width: 1280, Height: 800, Adaptive: true})
		child.attachTarget = id
		child.start()
		server.scheduleSaveLocked()
	}
	browser.start()
	return browser
}

func (server *headlessServer) browserTools(chat *serverChat) panelToolHandler {
	return func(ctx context.Context, name string, raw json.RawMessage) (panelToolResult, error) {
		var args browserToolArgs
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return panelToolResult{}, err
		}
		server.mu.Lock()
		server.ensureTabsLocked(chat)
		var browser *browserPage
		infos := []browserInfo{}
		index := -1
		for i, tab := range chat.tabs {
			if tab.browser != nil {
				info := tab.browser.snapshot()
				infos = append(infos, info)
				if args.TabID == "" || args.TabID == info.ID {
					browser = tab.browser
					index = i
				}
			}
		}
		if name == "browser_tabs" {
			switch args.Action {
			case "list":
				server.mu.Unlock()
				data, _ := json.Marshal(map[string]any{"tabs": infos})
				return panelToolResult{Text: string(data)}, nil
			case "open":
				address, err := browserURL(args.URL)
				if err != nil {
					server.mu.Unlock()
					return panelToolResult{}, err
				}
				browser = server.addBrowserLocked(chat, browserInfo{URL: address, Adaptive: true})
			case "select":
			case "close":
				if browser == nil {
					server.mu.Unlock()
					return panelToolResult{}, fmt.Errorf("Unknown browser tab")
				}
				browser.close()
				chat.tabs = append(chat.tabs[:index], chat.tabs[index+1:]...)
				server.scheduleSaveLocked()
				server.mu.Unlock()
				return panelToolResult{Text: "Browser tab closed."}, nil
			default:
				server.mu.Unlock()
				return panelToolResult{}, fmt.Errorf("Unknown tab action")
			}
		}
		server.scheduleSaveLocked()
		server.mu.Unlock()
		if browser == nil {
			return panelToolResult{}, fmt.Errorf("Open a browser tab with browser_tabs first")
		}
		if err := browser.run(ctx); err != nil {
			return panelToolResult{}, err
		}
		if name == "browser_tabs" {
			data, _ := json.Marshal(browser.snapshot())
			return panelToolResult{Text: string(data)}, nil
		}
		return executeBrowserTool(ctx, browser, name, args)
	}
}

type serverTerminal struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	id, dir string
	file    *os.File
	cancel  context.CancelFunc
	done    chan struct{}
	exit    *int
	output  []byte
	total   int64
	screen  vt10x.Terminal
}

func newServerTerminal(parent context.Context, dir string) (*serverTerminal, error) {
	ctx, cancel := context.WithCancel(parent)
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	command := exec.CommandContext(ctx, shell, "-i")
	command.Dir = dir
	command.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	file, err := pty.StartWithSize(command, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		cancel()
		return nil, err
	}
	terminal := &serverTerminal{
		id:     identifier()[:16],
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
		command.Wait()
		code := command.ProcessState.ExitCode()
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

func (terminal *serverTerminal) read(offset int64) map[string]any {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	first := terminal.total - int64(len(terminal.output))
	offset = max(first, min(offset, terminal.total))
	for offset < terminal.total && !utf8.RuneStart(terminal.output[offset-first]) {
		offset++
	}
	end := min(terminal.total, offset+8192)
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

func (server *headlessServer) terminalTools(chat *serverChat) panelToolHandler {
	return func(ctx context.Context, name string, raw json.RawMessage) (panelToolResult, error) {
		var args struct {
			Action             string
			SessionID          string `json:"session_id"`
			Cwd, Command, Text string
			Keys               []string
			Offset             int64
			WaitMS             int `json:"wait_ms"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return panelToolResult{}, err
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
				return panelToolResult{Text: string(data)}, nil
			case "open":
				dir := server.root
				var err error
				if args.Cwd != "" {
					dir, err = workspacePath(server.root, args.Cwd)
				}
				if err == nil {
					terminal, err = newServerTerminal(server.ctx, dir)
				}
				if err != nil {
					server.mu.Unlock()
					return panelToolResult{}, err
				}
				chat.tabs = append(chat.tabs, &serverTab{terminal: terminal})
			case "select":
			case "close":
				if terminal == nil {
					server.mu.Unlock()
					return panelToolResult{}, fmt.Errorf("Unknown terminal")
				}
				terminal.close()
				chat.tabs = append(chat.tabs[:index], chat.tabs[index+1:]...)
				server.mu.Unlock()
				return panelToolResult{Text: "Terminal closed."}, nil
			default:
				server.mu.Unlock()
				return panelToolResult{}, fmt.Errorf("Unknown terminal action")
			}
		}
		server.mu.Unlock()
		if terminal == nil {
			return panelToolResult{}, fmt.Errorf("Open a terminal with terminal_tabs first")
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
				key, err = terminalKey(name)
				if err != nil {
					break
				}
				err = terminal.write(key)
				if err != nil {
					break
				}
			}
		case "terminal_screen":
			return panelToolResult{Text: "Terminal screenshot", Images: []ImagePart{{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(terminal.capture())}}}, nil
		case "terminal_tabs", "terminal_read":
		default:
			return panelToolResult{}, fmt.Errorf("Unknown terminal tool")
		}
		if err != nil {
			return panelToolResult{}, err
		}
		if args.WaitMS > 0 {
			timer := time.NewTimer(time.Duration(min(5000, args.WaitMS)) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return panelToolResult{}, ctx.Err()
			case <-timer.C:
			case <-terminal.done:
			}
		}
		data, _ := json.Marshal(terminal.read(args.Offset))
		return panelToolResult{Text: string(data)}, nil
	}
}

func (terminal *serverTerminal) resize(cols, rows int) error {
	if err := pty.Setsize(terminal.file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return err
	}
	terminal.screen.Resize(cols, rows)
	return nil
}
