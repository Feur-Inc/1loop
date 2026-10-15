//go:build !server

package main

// Terminal Use on the desktop: the model's terminal tools act on the chat's
// terminal tabs in the side panel.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"
)

type terminalOutput struct {
	file     io.Reader
	terminal *panelTerminal
}

// Keep raw ANSI output alongside the terminal renderer so a model can read
// incremental output without rebuilding or scraping the visible screen.
func (output *terminalOutput) Read(p []byte) (int, error) {
	n, err := output.file.Read(p)
	if n > 0 {
		terminal := output.terminal
		terminal.mu.Lock()
		terminal.total += int64(n)
		terminal.output = append(terminal.output, p[:n]...)
		if len(terminal.output) > 256<<10 {
			terminal.output = append([]byte(nil), terminal.output[len(terminal.output)-(256<<10):]...)
		}
		terminal.mu.Unlock()
	}
	return n, err
}

func (terminal *panelTerminal) read(offset int64) string {
	terminal.mu.Lock()
	defer terminal.mu.Unlock()
	first := terminal.total - int64(len(terminal.output))
	offset = max(first, min(offset, terminal.total))
	end := min(terminal.total, offset+8192)
	data, _ := json.Marshal(map[string]any{
		"session_id":     terminal.id,
		"cwd":            terminal.dir,
		"output":         string(terminal.output[offset-first : end-first]),
		"available_from": first,
		"next_offset":    end,
		"total":          terminal.total,
		"exit_code":      terminal.exit,
	})
	return string(data)
}

func (ui *desktopUI) terminalTools(chat *conversation) panelToolHandler {
	return func(ctx context.Context, name string, raw json.RawMessage) (panelToolResult, error) {
		var args struct {
			Action             string
			SessionID          string `json:"session_id"`
			Cwd, Command, Text string
			Keys               []string
			WaitMS             int `json:"wait_ms"`
			Offset             int64
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return panelToolResult{}, err
		}
		var term *panelTerminal
		var problem error
		var list []map[string]any
		var screen image.Image
		ui.onUI(ctx, func() {
			panel := ui.panel
			state := panel.stateOf(chat)
			tabs, active := state.tabs, state.active
			if chat == panel.owner {
				tabs, active = panel.tabs, panel.active
			}
			at := -1
			for i, tab := range tabs {
				if tab.terminal != nil {
					list = append(list, map[string]any{"session_id": tab.terminal.id, "cwd": tab.terminal.dir})
					if tab.terminal.id == args.SessionID {
						at = i
					}
				}
			}
			if args.SessionID == "" {
				if active >= 0 && active < len(tabs) && tabs[active].terminal != nil {
					at = active
				} else {
					for i := len(tabs) - 1; i >= 0; i-- {
						if tabs[i].terminal != nil {
							at = i
							break
						}
					}
				}
			}
			update := func() {
				state.tabs, state.active, state.open = tabs, active, true
				chat.Panel = state.saved()
				if chat == panel.owner {
					panel.tabs, panel.active = tabs, active
					panel.render()
					if panel.target == 0 {
						panel.unfold()
					}
				}
				ui.save()
			}
			if name == "terminal_tabs" {
				switch args.Action {
				case "list":
					return
				case "open":
					dir := chat.Config.Workspace
					if args.Cwd != "" {
						dir, problem = workspacePath(dir, args.Cwd)
						if problem != nil {
							return
						}
					}
					tab, err := panel.terminalTab(dir)
					if err != nil {
						problem = err
						return
					}
					tabs = append(tabs, tab)
					at = len(tabs) - 1
				case "close":
					if at < 0 {
						problem = fmt.Errorf("No terminal with this ID in this chat")
						return
					}
					tabs[at].dispose()
					tabs = append(tabs[:at], tabs[at+1:]...)
					active = min(active, len(tabs)-1)
					update()
					return
				case "select":
				default:
					problem = fmt.Errorf("Unknown terminal tab action")
					return
				}
			}
			if at < 0 {
				problem = fmt.Errorf("No terminal in this chat; open one with terminal_tabs first")
				return
			}
			term = tabs[at].terminal
			active = at
			update()
			if name == "terminal_screen" {
				if chat != panel.owner {
					problem = fmt.Errorf("Open this chat to capture its terminal; terminal_read works in the background")
					return
				}
				driver := fyne.CurrentApp().Driver()
				canvas := ui.window.Canvas()
				pos := driver.AbsolutePositionForObject(term.widget).Subtract(driver.AbsolutePositionForObject(tabs[at].content))
				scale := canvas.Scale()
				// Render this tab directly: reading the GL front buffer can yield
				// black frames on composited or virtual displays.
				captureCanvas := software.NewCanvas()
				captureCanvas.SetPadded(false)
				captureCanvas.SetScale(scale)
				captureCanvas.Resize(tabs[at].content.Size())
				captureCanvas.SetContent(tabs[at].content)
				capture := captureCanvas.Capture()
				bounds := image.Rect(int(pos.X*scale), int(pos.Y*scale), int((pos.X+term.widget.Size().Width)*scale), int((pos.Y+term.widget.Size().Height)*scale)).Intersect(capture.Bounds())
				if bounds.Empty() {
					problem = fmt.Errorf("Terminal is not visible yet; retry after its tab opens")
					return
				}
				im := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
				draw.Draw(im, im.Bounds(), capture, bounds.Min, draw.Src)
				screen = im
			}
		})
		if ctx.Err() != nil {
			return panelToolResult{}, ctx.Err()
		}
		if problem != nil {
			return panelToolResult{}, problem
		}
		encode := func(v any) string { data, _ := json.Marshal(v); return string(data) }
		if name == "terminal_tabs" {
			if args.Action == "list" {
				return panelToolResult{Text: encode(map[string]any{"terminals": list})}, nil
			}
			if args.Action == "close" {
				return panelToolResult{Text: "Terminal closed."}, nil
			}
			return panelToolResult{Text: encode(map[string]string{"session_id": term.id, "cwd": term.dir})}, nil
		}
		if name == "terminal_screen" {
			var data bytes.Buffer
			if err := png.Encode(&data, screen); err != nil {
				return panelToolResult{}, err
			}
			return panelToolResult{Text: "Integrated terminal screenshot: " + term.id, Images: []ImagePart{{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(data.Bytes())}}}, nil
		}
		if name == "terminal_read" {
			return panelToolResult{Text: term.read(args.Offset)}, nil
		}
		var text string
		switch name {
		case "terminal_command":
			text = args.Command + "\r"
		case "terminal_write":
			text = args.Text
		case "terminal_keys":
			for _, name := range args.Keys {
				key, err := terminalKey(name)
				if err != nil {
					return panelToolResult{}, err
				}
				text += key
			}
		default:
			return panelToolResult{}, fmt.Errorf("Unknown terminal tool")
		}
		term.mu.Lock()
		offset := term.total
		exited := term.exit != nil
		term.mu.Unlock()
		if exited {
			return panelToolResult{}, fmt.Errorf("Terminal has exited")
		}
		if _, err := io.WriteString(term.file, text); err != nil {
			return panelToolResult{}, err
		}
		if args.WaitMS > 0 {
			timer := time.NewTimer(time.Duration(min(args.WaitMS, 5000)) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-term.done:
			case <-ctx.Done():
				return panelToolResult{}, ctx.Err()
			}
		}
		return panelToolResult{Text: term.read(offset)}, nil
	}
}
