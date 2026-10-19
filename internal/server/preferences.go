package server

import (
	"1loop/internal/core"
	"net/http"
	"strings"
)

func (server *headlessServer) setPreferences(w http.ResponseWriter, r *http.Request) {
	var args struct{ UserPreferences string }
	if core.ReadJSON(r, &args) != nil || len(args.UserPreferences) > 65536 {
		core.Fail(w, 400, "Preferences must be at most 64 KiB")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	previous := server.instructions
	server.instructions = strings.TrimSpace(args.UserPreferences)
	if err := server.saveSettingsLocked(); err != nil {
		server.instructions = previous
		core.Fail(w, 500, "Unable to save preferences")
		return
	}
	server.broadcastLocked("settings", server.settingsLocked())
	core.WriteJSON(w, 200, map[string]any{"userPreferences": server.instructions})
}
