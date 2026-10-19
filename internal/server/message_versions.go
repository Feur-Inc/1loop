package server

import (
	"net/http"
	"strings"

	"1loop/internal/agent"
	"1loop/internal/core"
)

func (chat *serverChat) branching() agent.VersionState {
	return agent.VersionState{Config: chat.Config, History: chat.History, Items: chat.Items, Version: chat.Version, Versions: chat.Versions}
}

func (chat *serverChat) applyBranch(state agent.VersionState) {
	chat.Config, chat.History, chat.Items = state.Config, state.History, state.Items
	chat.Version, chat.Versions = state.Version, state.Versions
}

func (server *headlessServer) replaceLocked(chat *serverChat) {
	server.broadcastLocked("replace", map[string]string{"chat": chat.ID})
	server.changedLocked(chat, -1)
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
}

func (server *headlessServer) editMessage(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Index        int
		Action, Text string
	}
	if core.ReadJSON(r, &args) != nil || (args.Action != "edit" && args.Action != "retry") {
		core.Fail(w, 400, "Invalid message action")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.chatLocked(r.PathValue("id"))
	if chat == nil || args.Index < 0 || args.Index >= len(chat.Items) {
		core.Fail(w, 404, "Message not found")
		return
	}
	if chat.run != nil {
		core.Fail(w, 409, "Stop the current response before editing or retrying")
		return
	}
	index := args.Index
	if args.Action == "retry" {
		for index >= 0 && chat.Items[index].Kind != "user" {
			index--
		}
	}
	if index < 0 || chat.Items[index].Kind != "user" {
		core.Fail(w, 400, "Choose a user message")
		return
	}
	item := chat.Items[index]
	text := item.Text
	if args.Action == "edit" {
		text = strings.TrimSpace(args.Text)
	}
	if text == "" && len(item.Images) == 0 {
		core.Fail(w, 400, "Nothing to send")
		return
	}
	state := chat.branching()
	id := state.ForkMessage(index, args.Action == "retry")
	chat.applyBranch(state)
	chat.queue, chat.steer = nil, nil
	chat.Cache, chat.review = &agent.ContextCache{}, nil
	server.replaceLocked(chat)
	server.runWithIDLocked(chat, agent.Message{Role: "user", Content: text, Images: item.Images}, id)
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *headlessServer) selectVersion(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Anchor string
		Delta  int
	}
	if core.ReadJSON(r, &args) != nil || (args.Delta != -1 && args.Delta != 1) {
		core.Fail(w, 400, "Invalid version")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.chatLocked(r.PathValue("id"))
	if chat == nil {
		core.Fail(w, 404, "Chat not found")
		return
	}
	if chat.run != nil {
		core.Fail(w, 409, "Stop the current response before switching versions")
		return
	}
	state := chat.branching()
	if !state.SelectVersion(args.Anchor, args.Delta) {
		core.Fail(w, 400, "Version not found")
		return
	}
	chat.applyBranch(state)
	chat.queue, chat.steer = nil, nil
	chat.Cache, chat.review = &agent.ContextCache{}, nil
	server.replaceLocked(chat)
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}
