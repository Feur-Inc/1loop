// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/settings"
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

func (server *headlessServer) setMemory(w http.ResponseWriter, r *http.Request) {
	var args struct{ Enabled bool }
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Say whether memory is on")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	server.memoryOff = !args.Enabled
	if err := server.saveSettingsLocked(); err != nil {
		server.memoryOff = args.Enabled
		core.Fail(w, 500, "Unable to save the setting")
		return
	}
	server.broadcastLocked("settings", server.settingsLocked())
	core.WriteJSON(w, 200, map[string]any{"memory": args.Enabled})
}

func (server *headlessServer) setWrapUp(w http.ResponseWriter, r *http.Request) {
	var args struct{ Enabled bool }
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Say whether the next-step card is on")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	server.wrapUpOff = !args.Enabled
	if err := server.saveSettingsLocked(); err != nil {
		server.wrapUpOff = args.Enabled
		core.Fail(w, 500, "Unable to save the setting")
		return
	}
	server.broadcastLocked("settings", server.settingsLocked())
	core.WriteJSON(w, 200, map[string]any{"wrapUp": args.Enabled})
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

// settingsAccess is the settings tool's way to this server's own settings
// (internal/settings): those it has, each change saved and sent to the
// connected apps like one made in their Settings.
func (server *headlessServer) settingsAccess() *agent.SettingsAccess {
	onOff := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}
	return &agent.SettingsAccess{
		Get: func(_ context.Context, key string) (string, bool) {
			server.mu.Lock()
			defer server.mu.Unlock()
			switch key {
			case settings.KeyMemory:
				return onOff(!server.memoryOff), true
			case settings.KeyWrapUp:
				return onOff(!server.wrapUpOff), true
			case settings.KeyDynamicCompaction:
				return onOff(!server.classic), true
			case settings.KeyAutoResume:
				return onOff(server.autoResume != nil && server.autoResume.Enabled), true
			}
			return "", false
		},
		Set: func(ctx context.Context, key, value string) error {
			server.mu.Lock()
			defer server.mu.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			on := value == "on"
			memoryOff, wrapUpOff, classic, resume := server.memoryOff, server.wrapUpOff, server.classic, server.autoResume
			switch key {
			case settings.KeyMemory:
				server.memoryOff = !on
			case settings.KeyWrapUp:
				server.wrapUpOff = !on
			case settings.KeyDynamicCompaction:
				server.classic = !on
			case settings.KeyAutoResume:
				if on && (resume == nil || len(resume.Steps) == 0) {
					return fmt.Errorf("Automatic resumption has no steps yet: the user chooses them in Settings first")
				}
				next := agent.AutoResume{Enabled: on}
				if resume != nil {
					next.Steps = resume.Steps
				}
				server.autoResume = &next
			default:
				return fmt.Errorf("%s is not a setting of this server", key)
			}
			if err := server.saveSettingsLocked(); err != nil {
				server.memoryOff, server.wrapUpOff, server.classic, server.autoResume = memoryOff, wrapUpOff, classic, resume
				return err
			}
			server.broadcastLocked("settings", server.settingsLocked())
			return nil
		},
	}
}
