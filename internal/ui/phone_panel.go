package ui

// The phone's view of a chat's side panel: the same tabs as on the computer
// (task list, browsers, terminals, Git changes, tool calls), read live, and
// the browsers and terminals driven from the phone as from the window.
//
//   GET  /api/chats/{id}/panel                tabs: [{key, kind, title, url}]
//   GET  /api/chats/{id}/panel/{key}          a terminal's output, the changes' diff, a browser's page
//   GET  /api/chats/{id}/panel/{key}/screen   a browser's page as a JPEG
//   POST /api/chats/{id}/panel/{key}/input    {action, x, y, dy, text}
//
// A browser's x and y are shares of the page (0 to 1), as the phone shows
// it scaled. The task list itself travels with the chat's meta event.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

func todoForPhone(chat *conversation) []remoteapi.Todo {
	out := []remoteapi.Todo{}
	for _, item := range chat.Todo.Snapshot() {
		task := remoteapi.Todo{ID: item.ID, Text: item.Text, Status: item.Status}
		if item.StartedAt != nil {
			task.Started = item.StartedAt.UnixMilli()
		}
		if item.CompletedAt != nil {
			task.Completed = item.CompletedAt.UnixMilli()
		}
		out = append(out, task)
	}
	return out
}

// tabsOf is a chat's panel tabs: those on screen when it is the open chat.
func (panel *toolPanel) tabsOf(chat *conversation) []panelTab {
	if chat == panel.owner {
		return panel.tabs
	}
	return panel.stateOf(chat).tabs
}

func tabKey(tab panelTab) string {
	switch {
	case tab.browser != nil:
		return "browser-" + tab.browser.Snapshot().ID
	case tab.terminal != nil:
		return "terminal-" + tab.terminal.id
	case tab.kind == "tool":
		return fmt.Sprintf("tool-%d", tab.index)
	}
	return tab.kind
}

func remoteTabOf(chat *conversation, tab panelTab) remoteapi.Tab {
	out := remoteapi.Tab{Key: tabKey(tab), Kind: tab.kind, Title: tab.title}
	switch {
	case tab.browser != nil:
		info := tab.browser.Snapshot()
		out.URL, out.Title = info.URL, info.Title
		if out.Title == "" {
			out.Title = "Browser"
		}
	case tab.kind == "tool":
		out.Index = tab.index
		if tab.index < len(chat.Items) {
			verb, target, _ := agent.DescribeTool(chat.Items[tab.index], false)
			out.Title = strings.TrimSpace(verb + " " + target)
		}
	case tab.kind == "todo":
		out.Title = "Tasks"
	case tab.kind == "changes":
		out.Title = "Changes"
	case tab.kind == "terminal":
		out.Title = "Terminal"
	}
	return out
}

// Home panels keep remote tools usable before the first prompt, without a saved chat.
// Called on the UI thread, like the other panel lookups.
func (server *remoteServer) panelChat(id string) *conversation {
	workspace, home := strings.CutPrefix(id, "home:")
	if !home {
		return server.ui.chatByID(id)
	}
	ui := server.ui
	authorized := false
	for _, root := range ui.projects {
		if folder, err := remoteapi.ProjectFolder(root, workspace, false); err == nil && folder == workspace {
			authorized = true
			break
		}
	}
	if !authorized {
		return nil
	}
	if chat := server.panelHomes[id]; chat != nil {
		return chat
	}
	if server.panelHomes == nil {
		server.panelHomes = map[string]*conversation{}
	}
	config := ui.cfg
	config.Workspace, config.Session, config.RanWith = workspace, "", nil
	chat := &conversation{ID: id, Config: config, panel: &panelState{active: -1}}
	chat.panel.tabs = []panelTab{ui.panel.changesTab(workspace)}
	server.panelHomes[id] = chat
	return chat
}

func (server *remoteServer) takePanelHome(workspace string) *conversation {
	if server == nil {
		return nil
	}
	id := "home:" + workspace
	chat := server.panelHomes[id]
	delete(server.panelHomes, id)
	return chat
}

// findTab looks a chat's tab up by its key, on the window's thread.
func (server *remoteServer) findTab(req *http.Request) (chat *conversation, tab panelTab, ok bool) {
	server.onUI(func() {
		chat = server.panelChat(req.PathValue("id"))
		if chat == nil || server.ui.panel == nil {
			return
		}
		for _, candidate := range server.ui.panel.tabsOf(chat) {
			if tabKey(candidate) == req.PathValue("key") {
				tab, ok = candidate, true
				return
			}
		}
	})
	return
}

func (server *remoteServer) panelTabs(writer http.ResponseWriter, req *http.Request) {
	var out map[string]any
	server.onUI(func() {
		chat := server.panelChat(req.PathValue("id"))
		if chat == nil || server.ui.panel == nil {
			return
		}
		tabs := []remoteapi.Tab{}
		for _, tab := range server.ui.panel.tabsOf(chat) {
			if tab.kind == "files" {
				continue // the phone browses files with its own explorer
			}
			tabs = append(tabs, remoteTabOf(chat, tab))
		}
		out = map[string]any{"tabs": tabs}
	})
	if out == nil {
		core.Fail(writer, http.StatusNotFound, "This chat no longer exists")
		return
	}
	core.WriteJSON(writer, http.StatusOK, out)
}

func (server *remoteServer) panelTab(writer http.ResponseWriter, req *http.Request) {
	chat, tab, ok := server.findTab(req)
	if !ok {
		core.Fail(writer, http.StatusNotFound, "This tab is closed")
		return
	}
	switch {
	case tab.terminal != nil:
		t := tab.terminal
		t.mu.Lock()
		raw := t.output
		if len(raw) > 48<<10 {
			raw = raw[len(raw)-48<<10:]
		}
		text := remoteapi.PlainTerminal(raw)
		exited := t.exit != nil
		t.mu.Unlock()
		core.WriteJSON(writer, http.StatusOK, map[string]any{"output": text, "cwd": t.dir, "exited": exited})
	case tab.browser != nil:
		info := tab.browser.Snapshot()
		core.WriteJSON(writer, http.StatusOK, info)
	case tab.kind == "changes":
		root := tab.workspace
		if root == "" {
			root = chat.Config.Workspace
		}
		diff, err := agent.WorkspaceDiff(req.Context(), root, false)
		if err != nil {
			core.Fail(writer, http.StatusInternalServerError, err.Error())
			return
		}
		core.WriteJSON(writer, http.StatusOK, map[string]any{"diff": diff})
	default:
		core.WriteJSON(writer, http.StatusOK, map[string]any{})
	}
}

func (server *remoteServer) panelScreen(writer http.ResponseWriter, req *http.Request) {
	_, tab, ok := server.findTab(req)
	if !ok || tab.browser == nil {
		core.Fail(writer, http.StatusNotFound, "This browser tab is closed")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 15*time.Second)
	defer cancel()
	var data []byte
	err := tab.browser.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		data, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatJpeg).WithQuality(72).WithFromSurface(true).Do(ctx)
		return err
	}))
	if err != nil {
		core.Fail(writer, http.StatusServiceUnavailable, err.Error())
		return
	}
	writer.Header().Set("Content-Type", "image/jpeg")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Write(data)
}

func (server *remoteServer) panelInput(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Action           string  `json:"action"`
		X                float64 `json:"x"`
		Y                float64 `json:"y"`
		DY               float64 `json:"dy"`
		Text             string  `json:"text"`
		Width, Height    int
		Mobile, Adaptive bool
		PixelRatio       float64
		DX               float64
	}
	if err := core.ReadJSON(req, &body); err != nil {
		core.Fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	_, tab, ok := server.findTab(req)
	if !ok {
		core.Fail(writer, http.StatusNotFound, "This tab is closed")
		return
	}
	var err error
	switch {
	case tab.terminal != nil:
		if body.Action == "resize" {
			if body.Width < 20 || body.Width > 400 || body.Height < 5 || body.Height > 150 {
				core.Fail(writer, 400, "Terminal size must be 20..400 columns and 5..150 rows")
				return
			}
			err = tab.terminal.file.Resize(body.Width, body.Height)
		} else {
			err = terminalInput(tab.terminal, body.Action, body.Text)
		}
	case tab.browser != nil:
		switch body.Action {
		case "resize":
			err = tab.browser.Resize(req.Context(), body.Width, body.Height, body.Mobile, body.Adaptive, body.PixelRatio)
		case "logs":
			core.WriteJSON(writer, 200, map[string]any{"logs": tab.browser.LogSnapshot(0)})
			return
		case "evaluate":
			tab.browser.AddLog("command", body.Text, "JavaScript")
			var value json.RawMessage
			value, err = tab.browser.Evaluate(req.Context(), body.Text)
			if err == nil {
				tab.browser.AddLog("result", string(value), "JavaScript")
				core.WriteJSON(writer, 200, map[string]any{"result": value})
				return
			}
		case "scroll":
			view := tab.browser.Snapshot()
			err = tab.browser.Interact(req.Context(), "scroll", "", "", body.X*float64(view.Width), body.Y*float64(view.Height), body.DX*float64(view.Width), body.DY*float64(view.Height))
		default:
			err = remoteapi.BrowserInputFromPhone(req.Context(), tab.browser, body.Action, body.X, body.Y, body.DY, body.Text)
		}
	default:
		err = fmt.Errorf("This tab takes no input")
	}
	if err != nil {
		core.Fail(writer, http.StatusBadRequest, err.Error())
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]any{"ok": true})
}

func terminalInput(t *panelTerminal, action, text string) error {
	t.mu.Lock()
	exited := t.exit != nil
	t.mu.Unlock()
	if exited {
		return fmt.Errorf("This terminal has exited")
	}
	switch action {
	case "line":
		text += "\r"
	case "key":
		key, err := agent.TerminalKey(text)
		if err != nil {
			return err
		}
		text = key
	case "text":
	default:
		return fmt.Errorf("Unknown terminal action")
	}
	_, err := t.file.Write([]byte(text))
	return err
}

func (server *remoteServer) editPanel(w http.ResponseWriter, r *http.Request) {
	var args struct{ Action, Kind, Key, URL string }
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	var chat *conversation
	server.onUI(func() { chat = server.panelChat(r.PathValue("id")) })
	if chat == nil {
		core.Fail(w, 404, "Chat not found")
		return
	}
	kind, id := args.Kind, ""
	if args.Action == "close" {
		kind, id, _ = strings.Cut(args.Key, "-")
	} else if args.Action != "open" {
		core.Fail(w, 400, "Choose open or close")
		return
	}
	var result agent.PanelToolResult
	var err error
	switch kind {
	case "browser":
		raw, _ := json.Marshal(map[string]string{"action": args.Action, "tab_id": id, "url": args.URL})
		result, err = server.ui.browserTools(chat)(r.Context(), "browser_tabs", raw)
	case "terminal":
		raw, _ := json.Marshal(map[string]string{"action": args.Action, "session_id": id})
		result, err = server.ui.terminalTools(chat)(r.Context(), "terminal_tabs", raw)
	default:
		core.Fail(w, 400, "Choose browser or terminal")
		return
	}
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]string{"result": result.Text})
}

func (server *remoteServer) panelStream(writer http.ResponseWriter, req *http.Request) {
	_, tab, ok := server.findTab(req)
	if !ok || tab.browser == nil {
		core.Fail(writer, 404, "This browser tab is closed")
		return
	}
	revoked := make(chan struct{})
	server.mu.Lock()
	server.streams[revoked] = true
	server.mu.Unlock()
	defer func() { server.mu.Lock(); delete(server.streams, revoked); server.mu.Unlock() }()
	browser.ServeBrowserStream(writer, req, tab.browser, revoked)
}
