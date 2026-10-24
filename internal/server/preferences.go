package server

import (
	"1loop/internal/core"
	"context"
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

// updatePreferences is the memory tool's way to the preferences: saved and
// sent to the phone like a change made in its settings.
func (server *headlessServer) updatePreferences(ctx context.Context, update func(string) (string, error)) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	next, err := update(server.instructions)
	if err != nil || next == server.instructions {
		return err
	}
	previous := server.instructions
	server.instructions = next
	if err := server.saveSettingsLocked(); err != nil {
		server.instructions = previous
		return err
	}
	server.broadcastLocked("settings", server.settingsLocked())
	return nil
}
