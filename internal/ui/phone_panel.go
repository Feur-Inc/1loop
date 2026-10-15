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

// findTab looks a chat's tab up by its key, on the window's thread.
func (server *remoteServer) findTab(req *http.Request) (chat *conversation, tab panelTab, ok bool) {
	server.onUI(func() {
		chat = server.ui.chatByID(req.PathValue("id"))
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
		chat := server.ui.chatByID(req.PathValue("id"))
		if chat == nil || server.ui.panel == nil {
			return
		}
		tabs := []remoteapi.Tab{}
		for _, tab := range server.ui.panel.tabsOf(chat) {
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
		core.WriteJSON(writer, http.StatusOK, map[string]any{"url": info.URL, "title": info.Title, "width": info.Width, "height": info.Height, "error": info.Error})
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
		Action string  `json:"action"`
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		DY     float64 `json:"dy"`
		Text   string  `json:"text"`
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
		err = terminalInput(tab.terminal, body.Action, body.Text)
	case tab.browser != nil:
		err = remoteapi.BrowserInputFromPhone(req.Context(), tab.browser, body.Action, body.X, body.Y, body.DY, body.Text)
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

func (server *remoteServer) panelStream(writer http.ResponseWriter, req *http.Request) {
	_, tab, ok := server.findTab(req)
	if !ok || tab.browser == nil {
		core.Fail(writer, 404, "This browser tab is closed")
		return
	}
	browser.ServeBrowserStream(writer, req, tab.browser, nil)
}
