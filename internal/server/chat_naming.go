// SPDX-License-Identifier: MPL-2.0

package server

import (
	"errors"
	"log"
	"net/http"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

// setTitleLocked gives chat a title (a client's Rename, the agent's chats
// tool or the naming model), saved and sent to every connected client.
func (server *headlessServer) setTitleLocked(chat *serverChat, title string) error {
	chat.Title = title
	server.scheduleSaveLocked() // written apart: a rename must not hold the lock (#520)
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	return nil
}

// renameLocked gives chat the title its agent chose with the chats tool.
func (server *headlessServer) renameLocked(chat *serverChat, title string) error {
	if server.chatLocked(chat.ID) != chat {
		return errors.New("This conversation was deleted")
	}
	if title = remoteapi.CleanChatTitle(title); title == "" {
		return errors.New("Give the new title as text")
	}
	return server.setTitleLocked(chat, title)
}

// namingChoiceLocked is the chat naming model, when one is chosen and
// still offered by an enabled connection.
func (server *headlessServer) namingChoiceLocked() (agent.ModelChoice, bool) {
	model := server.namingModel
	if !model.Set() {
		return agent.ModelChoice{}, false
	}
	for _, choice := range server.choices {
		if choice.Connection.ID == model.ConnectionID && choice.ID == model.Model && choice.Connection.Enabled {
			return choice, true
		}
	}
	return agent.ModelChoice{}, false
}

// nameNewChatLocked has the naming model, when one is chosen, sum a new
// chat's first prompt up into its title: only the words typed, never its
// pictures or files (agent.NamingPrompt). The title reaches the clients
// when it comes, unless the chat was renamed or deleted meanwhile.
func (server *headlessServer) nameNewChatLocked(chat *serverChat, prompt agent.Message) {
	choice, ok := server.namingChoiceLocked()
	if !ok || prompt.From != nil || prompt.Internal || agent.NamingPrompt(prompt).Content == "" {
		return
	}
	cfg := agent.ResumeConfig(chat.Config, choice, server.keys[choice.Connection.ID], "")
	placeholder := chat.Title
	go func() {
		title, err := server.backend.NameChat(server.ctx, cfg, prompt)
		if err != nil {
			log.Printf("chat naming model: %v", err)
			return
		}
		server.mu.Lock()
		defer server.mu.Unlock()
		if server.chatLocked(chat.ID) == chat && chat.Title == placeholder {
			if title = remoteapi.CleanChatTitle(title); title != "" {
				_ = server.setTitleLocked(chat, title)
			}
		}
	}()
}

// setNamingModel chooses the model that names new chats; a null model
// turns automatic naming off.
func (server *headlessServer) setNamingModel(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Model *agent.NamingModel `json:"model"`
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	previous := server.namingModel
	if !args.Model.Set() {
		args.Model = nil
	}
	server.namingModel = args.Model
	if args.Model != nil {
		if _, ok := server.namingChoiceLocked(); !ok {
			server.namingModel = previous
			core.Fail(w, 400, "This model is unavailable")
			return
		}
	}
	if err := server.saveSettingsLocked(); err != nil {
		server.namingModel = previous
		core.Fail(w, 500, "Unable to save the setting")
		return
	}
	server.broadcastLocked("settings", server.settingsLocked())
	core.WriteJSON(w, 200, map[string]any{"namingModel": server.namingModel})
}
