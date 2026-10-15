//go:build !server

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Resolve tabs on the UI thread, but run Chromium work off that thread.
// The returned handler captures one conversation, never the currently open chat.
func (ui *desktopUI) browserTools(chat *conversation) panelToolHandler {
	return func(ctx context.Context, name string, raw json.RawMessage) (panelToolResult, error) {
		var args browserToolArgs
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&args); err != nil {
			return panelToolResult{}, err
		}
		var browser *browserPage
		var infos []browserInfo
		var problem error
		ui.onUI(ctx, func() {
			panel := ui.panel
			state := panel.stateOf(chat)
			tabs, active := state.tabs, state.active
			if chat == panel.owner {
				tabs, active = panel.tabs, panel.active
			}
			at := -1
			for i, tab := range tabs {
				if tab.browser == nil {
					continue
				}
				infos = append(infos, tab.browser.snapshot())
				if tab.browser.snapshot().ID == args.TabID && args.TabID != "" {
					at = i
				}
			}
			if args.TabID == "" {
				if active >= 0 && active < len(tabs) && tabs[active].browser != nil {
					at = active
				} else {
					for i := len(tabs) - 1; i >= 0; i-- {
						if tabs[i].browser != nil {
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
			if name == "browser_tabs" {
				switch args.Action {
				case "list":
					return
				case "open":
					url, err := browserURL(args.URL)
					if err != nil {
						problem = err
						return
					}
					tab := panel.browserTab(chat, browserInfo{URL: url, Adaptive: true}, tabs)
					tabs = append(tabs, tab)
					active = len(tabs) - 1
					browser = tab.browser
					update()
					return
				case "close":
					if at < 0 {
						problem = fmt.Errorf("No browser tab with this ID in this chat")
						return
					}
					tabs[at].dispose()
					tabs = append(tabs[:at], tabs[at+1:]...)
					active = min(active, len(tabs)-1)
					update()
					return
				case "select":
					if at < 0 {
						problem = fmt.Errorf("No browser tab with this ID in this chat")
						return
					}
					active = at
					browser = tabs[at].browser
					update()
					return
				default:
					problem = fmt.Errorf("Unknown browser tab action")
					return
				}
			}
			if at < 0 {
				problem = fmt.Errorf("No browser tab in this chat; open one with browser_tabs first")
				return
			}
			browser = tabs[at].browser
			active = at
			update()
		})
		if ctx.Err() != nil {
			return panelToolResult{}, ctx.Err()
		}
		if problem != nil {
			return panelToolResult{}, problem
		}
		encode := func(v any) string { data, _ := json.Marshal(v); return string(data) }
		if name == "browser_tabs" {
			if args.Action == "list" {
				return panelToolResult{Text: encode(map[string]any{"tabs": infos})}, nil
			}
			if args.Action == "close" {
				return panelToolResult{Text: "Browser tab closed."}, nil
			}
			if err := browser.run(ctx); err != nil {
				return panelToolResult{}, err
			}
			return panelToolResult{Text: encode(browser.snapshot())}, nil
		}
		return executeBrowserTool(ctx, browser, name, args)
	}
}
