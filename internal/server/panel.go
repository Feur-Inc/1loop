package server

// The headless server's side panel API: a chat's tabs, terminal screens,
// browser streams and the input sent to them.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

// Home panels are scoped to an authorized project, without creating a conversation.
func (server *headlessServer) panelChatLocked(id string) *serverChat {
	if !strings.HasPrefix(id, "home:") {
		return server.chatLocked(id)
	}
	workspace, err := server.workspaceLocked(strings.TrimPrefix(id, "home:"))
	if err != nil || server.closing {
		return nil
	}
	if home := server.panelHomes[id]; home != nil {
		return home
	}
	if server.panelHomes == nil {
		server.panelHomes = map[string]*serverChat{}
	}
	config := server.cfg
	config.Workspace, config.Session, config.RanWith = workspace, "", nil
	home := &serverChat{ID: id, Config: config, Todo: &agent.TodoState{}, Cache: &agent.ContextCache{}}
	server.panelHomes[id] = home
	return home
}

func (server *headlessServer) panelContextsLocked() []*serverChat {
	contexts := append([]*serverChat{}, server.chats...)
	for _, home := range server.panelHomes {
		contexts = append(contexts, home)
	}
	return contexts
}

func (server *headlessServer) findTab(req *http.Request) (*serverChat, *serverTab) {
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.panelChatLocked(req.PathValue("id"))
	if chat == nil {
		return nil, nil
	}
	server.ensureTabsLocked(chat)
	for _, tab := range chat.tabs {
		if tab.key() == req.PathValue("key") {
			return chat, tab
		}
	}
	return chat, nil
}

func (server *headlessServer) panelTabs(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.panelChatLocked(r.PathValue("id"))
	if chat == nil {
		core.Fail(w, 404, "Chat not found")
		return
	}
	server.ensureTabsLocked(chat)
	tabs := []remoteapi.Tab{}
	if !strings.HasPrefix(chat.ID, "home:") {
		tabs = append(tabs, remoteapi.Tab{Key: "todo", Kind: "todo", Title: "Tasks"})
	}
	tabs = append(tabs, remoteapi.Tab{Key: "changes", Kind: "changes", Title: "Changes"})
	if server.bridge != nil && chat.Config.Provider == "chatgptweb" && !strings.HasPrefix(chat.ID, "home:") {
		tabs = append(tabs, remoteapi.Tab{Key: "chatgptweb", Kind: "chatgptweb", Title: "ChatGPT"})
	}
	for _, tab := range chat.tabs {
		item := remoteapi.Tab{Key: tab.key()}
		if tab.browser != nil {
			info := tab.browser.Snapshot()
			item.Kind, item.Title, item.URL = "browser", info.Title, info.URL
			if item.Title == "" {
				item.Title = "Browser"
			}
		} else {
			item.Kind, item.Title = "terminal", "Terminal"
		}
		tabs = append(tabs, item)
	}
	core.WriteJSON(w, 200, map[string]any{"tabs": tabs})
}

func (server *headlessServer) panelTab(w http.ResponseWriter, r *http.Request) {
	chat, tab := server.findTab(r)
	if chat == nil {
		core.Fail(w, 404, "Chat not found")
		return
	}
	key := r.PathValue("key")
	if key == "changes" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		output, err := agent.WorkspaceDiff(ctx, chat.Config.Workspace, r.URL.Query().Get("staged") == "1")
		if err != nil {
			core.Fail(w, 400, err.Error())
			return
		}
		core.WriteJSON(w, 200, map[string]string{"diff": output})
		return
	}
	if strings.HasPrefix(key, "tool-") {
		index, err := strconv.Atoi(strings.TrimPrefix(key, "tool-"))
		server.mu.Lock()
		defer server.mu.Unlock()
		if err != nil || index < 0 || index >= len(chat.Items) {
			core.Fail(w, 404, "Tool not found")
			return
		}
		core.WriteJSON(w, 200, server.itemLocked(chat, index))
		return
	}
	if tab == nil {
		core.Fail(w, 404, "Tab closed")
		return
	}
	if tab.browser != nil {
		core.WriteJSON(w, 200, tab.browser.Snapshot())
		return
	}
	if r.URL.Query().Get("raw") == "1" {
		offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		core.WriteJSON(w, 200, tab.terminal.read(offset, 8192))
		return
	}
	tab.terminal.mu.Lock()
	output := remoteapi.PlainTerminal(tab.terminal.output)
	exited := tab.terminal.exit != nil
	tab.terminal.mu.Unlock()
	core.WriteJSON(w, 200, map[string]any{"output": output, "cwd": tab.terminal.dir, "exited": exited})
}

func (server *headlessServer) panelScreen(w http.ResponseWriter, r *http.Request) {
	_, tab := server.findTab(r)
	if tab == nil {
		core.Fail(w, 404, "Tab closed")
		return
	}
	var data []byte
	if tab.browser != nil {
		capture, err := tab.browser.Capture(r.Context(), r.URL.Query().Get("full") == "1")
		if err != nil {
			core.Fail(w, 503, err.Error())
			return
		}
		data, _ = base64.StdEncoding.DecodeString(capture.Data)
	} else {
		data = tab.terminal.capture()
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

func (server *headlessServer) panelStream(w http.ResponseWriter, r *http.Request) {
	_, tab := server.findTab(r)
	if tab == nil || tab.browser == nil {
		core.Fail(w, 404, "Browser closed")
		return
	}
	revoked := make(chan struct{})
	server.mu.Lock()
	server.streams[revoked] = true
	server.mu.Unlock()
	defer func() { server.mu.Lock(); delete(server.streams, revoked); server.mu.Unlock() }()
	browser.ServeBrowserStream(w, r, tab.browser, revoked)
}

func (server *headlessServer) panelInput(w http.ResponseWriter, r *http.Request) {
	_, tab := server.findTab(r)
	if tab == nil {
		core.Fail(w, 404, "Tab closed")
		return
	}
	var args struct {
		Action, Text     string
		X, Y, DX, DY     float64
		Width, Height    int
		Mobile, Adaptive bool
		PixelRatio       float64
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid input")
		return
	}
	var err error
	if tab.browser != nil {
		switch args.Action {
		case "evaluate":
			var value json.RawMessage
			tab.browser.AddLog("command", args.Text, "JavaScript")
			value, err = tab.browser.Evaluate(r.Context(), args.Text)
			if err == nil {
				tab.browser.AddLog("result", string(value), "JavaScript")
				core.WriteJSON(w, 200, map[string]any{"result": json.RawMessage(value)})
				return
			}
		case "logs":
			core.WriteJSON(w, 200, map[string]any{"logs": tab.browser.LogSnapshot(0)})
			return
		case "resize":
			if args.PixelRatio > 0 {
				err = tab.browser.Resize(r.Context(), args.Width, args.Height, args.Mobile, args.Adaptive, args.PixelRatio)
			} else {
				err = tab.browser.Resize(r.Context(), args.Width, args.Height, args.Mobile, args.Adaptive)
			}
		case "scroll":
			view := tab.browser.Snapshot()
			width, height := float64(view.Width), float64(view.Height)
			err = tab.browser.Interact(r.Context(), "scroll", "", "", args.X*width, args.Y*height, args.DX*width, args.DY*height)
		case "key":
			err = tab.browser.Interact(r.Context(), "key", "", args.Text, 0, 0, 0, 0)
		default:
			err = remoteapi.BrowserInputFromPhone(r.Context(), tab.browser, args.Action, args.X, args.Y, args.DY, args.Text)
		}
	} else {
		switch args.Action {
		case "resize":
			if args.Width < 20 || args.Width > 400 || args.Height < 5 || args.Height > 150 {
				core.Fail(w, 400, "Terminal size must be 20..400 columns and 5..150 rows")
				return
			}
			err = tab.terminal.resize(args.Width, args.Height)
		case "text", "write":
			err = tab.terminal.write(args.Text)
		case "line":
			err = tab.terminal.write(args.Text + "\r")
		case "key":
			var key string
			key, err = agent.TerminalKey(args.Text)
			if err == nil {
				err = tab.terminal.write(key)
			}
		default:
			err = fmt.Errorf("Unknown terminal input")
		}
	}
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}
