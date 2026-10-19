package ui

import (
	"net/http"
	"strings"

	"1loop/internal/agent"
	"1loop/internal/core"
)

func (server *remoteServer) editMessage(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Index        int
		Action, Text string
	}
	if core.ReadJSON(r, &args) != nil || (args.Action != "edit" && args.Action != "retry") {
		core.Fail(w, 400, "Invalid message action")
		return
	}
	problem := "Chat not found"
	server.onUI(func() {
		chat := server.ui.chatByID(r.PathValue("id"))
		if chat == nil {
			return
		}
		if chat.run != nil {
			problem = "Stop the current response before editing or retrying"
			return
		}
		index := args.Index
		if index < 0 || index >= len(chat.Items) {
			problem = "Message not found"
			return
		}
		if args.Action == "retry" {
			index = userItemBefore(chat, index)
		}
		if index < 0 || chat.Items[index].Kind != "user" {
			problem = "Choose a user message"
			return
		}
		item := chat.Items[index]
		text := item.Text
		if args.Action == "edit" {
			text = strings.TrimSpace(args.Text)
		}
		if text == "" && len(item.Images) == 0 {
			problem = "Nothing to send"
			return
		}
		server.ui.openChat(chat)
		if problem = server.ui.notReady(); problem != "" {
			return
		}
		server.ui.startVersion(chat, index, agent.Message{Role: "user", Content: text, Images: item.Images}, args.Action == "retry")
	})
	if problem != "" {
		core.Fail(w, 409, problem)
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func (server *remoteServer) selectVersion(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Anchor string
		Delta  int
	}
	if core.ReadJSON(r, &args) != nil || (args.Delta != -1 && args.Delta != 1) {
		core.Fail(w, 400, "Invalid version")
		return
	}
	problem := "Chat not found"
	server.onUI(func() {
		chat := server.ui.chatByID(r.PathValue("id"))
		if chat == nil {
			return
		}
		if chat.run != nil {
			problem = "Stop the current response before switching versions"
			return
		}
		if !chat.selectVersion(args.Anchor, args.Delta) {
			problem = "Version not found"
			return
		}
		server.ui.reloadChat(chat)
		server.ui.save()
		server.replaceChat(chat)
		problem = ""
	})
	if problem != "" {
		core.Fail(w, 409, problem)
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}
