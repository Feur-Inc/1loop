package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"1loop/internal/core"
	"1loop/internal/terminal"
)

type mobileTerminal struct {
	bridge  *mobileBridge
	startup context.Context
	root    bool // a root shell of Debian, outside the projects
}

func mobileTerminalHost(bridge *mobileBridge, startup context.Context) terminal.NativeHost {
	if bridge == nil {
		return nil
	}
	return &mobileTerminal{bridge: bridge, startup: startup}
}
func (host *mobileTerminal) Start(ctx context.Context, workspace, tty string, yolo bool) (string, error) {
	id := core.Identifier()[:16]
	// Bound the IPC handshake, not the lifetime of the persistent terminal.
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(host.startup, cancel)
	defer stop()
	err := host.call(startup, "open", id, map[string]any{"workspace": workspace, "tty": tty, "yolo": yolo, "root": host.root}, &struct{}{})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		host.Close(cleanup, id)
	}
	return id, err
}
func (host *mobileTerminal) call(ctx context.Context, action, id string, values map[string]any, result any) error {
	if values == nil {
		values = map[string]any{}
	}
	values["action"], values["id"] = action, id
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return host.bridge.call(ctx, "terminal_native", raw, result)
}
func (host *mobileTerminal) Wait(ctx context.Context, id string) (int, error) {
	var result struct {
		Exit int `json:"exit"`
	}
	err := host.call(ctx, "wait", id, nil, &result)
	return result.Exit, err
}
func (host *mobileTerminal) Close(ctx context.Context, id string) error {
	return host.call(ctx, "close", id, nil, &struct{}{})
}

// openDebianTerminal opens a root shell of the phone's Debian in a home
// panel, for the phone's user only: the agent works in chats and never sees
// it, nor do the devices the phone is shared with (findTab, panelTabs).
func (server *headlessServer) openDebianTerminal(r *http.Request, chat *serverChat) (string, error) {
	ctx := r.Context()
	server.mu.Lock()
	revision, bridge := server.terminalRevision, server.bridge
	server.mu.Unlock()
	if bridge == nil || !strings.HasPrefix(chat.ID, "home:") || !server.phoneApp(r) {
		return "", fmt.Errorf("The Debian terminal exists only in the phone's app, outside chats")
	}
	terminal, err := newServerTerminal(server.ctx, "/root", &mobileTerminal{bridge: bridge, startup: ctx, root: true})
	if err != nil {
		return "", err
	}
	terminal.root = true
	server.mu.Lock()
	if server.closing || server.ctx.Err() != nil || ctx.Err() != nil || revision != server.terminalRevision || server.panelChatLocked(chat.ID) != chat {
		server.mu.Unlock()
		terminal.close()
		return "", fmt.Errorf("Terminal startup was cancelled or permissions changed")
	}
	tab := &serverTab{terminal: terminal}
	chat.tabs = append(chat.tabs, tab)
	server.mu.Unlock()
	return tab.key(), nil
}
