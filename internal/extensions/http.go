// SPDX-License-Identifier: MPL-2.0

package extensions

// The extensions' routes, the same on a server, on the phone's own engine
// and on the desktop the phone drives.

import (
	"context"
	"net/http"
	"os"
	"slices"
	"time"

	"1loop/internal/core"
)

// PluginView is an installed plugin with what it brings.
type PluginView struct {
	Plugin
	Skills   []string `json:"skills"`
	MCP      []string `json:"mcp"`
	Hooks    int      `json:"hooks"`
	Commands int      `json:"commands"`
	Agents   int      `json:"agents"`
}

// Installed is what GET /api/extensions answers.
type Installed struct {
	Skills  []Skill      `json:"skills"`
	Plugins []PluginView `json:"plugins"`
	MCP     []MCPServer  `json:"mcp"`
	// PluginMCP are the enabled plugins' servers, which their plugin brings.
	PluginMCP []MCPServer `json:"pluginMcp"`
	Dir       string      `json:"dir"`
}

// Hidden stands for an MCP server's environment or header value in what
// the routes answer: those are often its token, which stays where the
// extensions are. Saved back unchanged, it keeps the stored value.
const Hidden = "••••••"

func hide(server MCPServer) MCPServer {
	hidden := func(values map[string]string) map[string]string {
		if values == nil {
			return nil
		}
		out := make(map[string]string, len(values))
		for name, value := range values {
			out[name] = value
			if value != "" {
				out[name] = Hidden
			}
		}
		return out
	}
	server.Env, server.Headers = hidden(server.Env), hidden(server.Headers)
	return server
}

// hidden is the view as the routes answer it.
func (view Installed) hidden() Installed {
	view.MCP = slices.Clone(view.MCP)
	for i := range view.MCP {
		view.MCP[i] = hide(view.MCP[i])
	}
	view.PluginMCP = slices.Clone(view.PluginMCP)
	for i := range view.PluginMCP {
		view.PluginMCP[i] = hide(view.PluginMCP[i])
	}
	return view
}

// reveal puts back the stored values a client saved as Hidden, from the
// server saved as previous (or under the same name).
func reveal(server MCPServer, previous string) MCPServer {
	if previous == "" {
		previous = server.Name
	}
	var saved MCPServer
	if registry, err := Load(); err == nil {
		for _, entry := range registry.MCP {
			if entry.Name == previous {
				saved = entry
			}
		}
	}
	restore := func(values, stored map[string]string) map[string]string {
		if values == nil {
			return nil
		}
		out := make(map[string]string, len(values))
		for name, value := range values {
			if value == Hidden {
				value = stored[name]
			}
			out[name] = value
		}
		return out
	}
	server.Env, server.Headers = restore(server.Env, saved.Env), restore(server.Headers, saved.Headers)
	return server
}

// View reads what is installed, for the settings.
func View() (Installed, error) {
	registry, err := Load()
	if err != nil {
		return Installed{}, err
	}
	view := Installed{Skills: registry.Skills, Plugins: []PluginView{}, MCP: registry.MCP, PluginMCP: []MCPServer{}, Dir: Dir()}
	if view.Skills == nil {
		view.Skills = []Skill{}
	}
	if view.MCP == nil {
		view.MCP = []MCPServer{}
	}
	for _, server := range Enabled().MCP {
		if server.Plugin != "" {
			view.PluginMCP = append(view.PluginMCP, server)
		}
	}
	for _, plugin := range registry.Plugins {
		contents := ReadPlugin(plugin.Dir)
		entry := PluginView{Plugin: plugin, Skills: []string{}, MCP: []string{}, Hooks: len(contents.Hooks), Commands: contents.Commands, Agents: contents.Agents}
		for _, skill := range contents.Skills {
			entry.Skills = append(entry.Skills, skill.Name)
		}
		for _, server := range contents.MCP {
			entry.MCP = append(entry.MCP, server.Name)
		}
		view.Plugins = append(view.Plugins, entry)
	}
	return view, nil
}

// Handlers are the routes, by "METHOD /path". changed runs after anything
// installed, removed or switched (the server tells its clients).
func Handlers(changed func()) map[string]http.HandlerFunc {
	done := func(w http.ResponseWriter, err error) {
		if err != nil {
			core.Fail(w, 400, err.Error())
			return
		}
		if changed != nil {
			changed()
		}
		view, err := View()
		if err != nil {
			core.Fail(w, 500, err.Error())
			return
		}
		core.WriteJSON(w, 200, view.hidden())
	}
	return map[string]http.HandlerFunc{
		"GET /api/extensions": func(w http.ResponseWriter, r *http.Request) {
			view, err := View()
			if err != nil {
				core.Fail(w, 500, err.Error())
				return
			}
			core.WriteJSON(w, 200, view.hidden())
		},
		"POST /api/extensions/search": func(w http.ResponseWriter, r *http.Request) {
			var args struct {
				Kind, Query, Sort string
				Page              int
			}
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			result, err := Search(ctx, args.Kind, args.Query, args.Sort, args.Page)
			if err != nil {
				core.Fail(w, 502, err.Error())
				return
			}
			core.WriteJSON(w, 200, result)
		},
		"POST /api/extensions/readme": func(w http.ResponseWriter, r *http.Request) {
			var args struct{ Repo string }
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			readme, err := Readme(ctx, args.Repo)
			if err != nil {
				core.Fail(w, 502, err.Error())
				return
			}
			core.WriteJSON(w, 200, map[string]string{"readme": readme})
		},
		"POST /api/extensions/prepare": func(w http.ResponseWriter, r *http.Request) {
			var args struct{ Repo string }
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
			defer cancel()
			plan, err := Prepare(ctx, args.Repo)
			if err != nil {
				core.Fail(w, 400, err.Error())
				return
			}
			core.WriteJSON(w, 200, plan)
		},
		"POST /api/extensions/install": func(w http.ResponseWriter, r *http.Request) {
			var args struct {
				Plan string
				Keep []string
			}
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			_, err := Install(args.Plan, args.Keep)
			done(w, err)
		},
		"POST /api/extensions/remove": func(w http.ResponseWriter, r *http.Request) {
			var args struct{ Kind, ID string }
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			done(w, Remove(args.Kind, args.ID))
		},
		"POST /api/extensions/enable": func(w http.ResponseWriter, r *http.Request) {
			var args struct {
				Kind, ID string
				Enabled  bool
			}
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			err := SetEnabled(args.Kind, args.ID, args.Enabled)
			if args.Kind == "mcp" {
				Servers.Forget(args.ID)
			}
			done(w, err)
		},
		"POST /api/extensions/mcp": func(w http.ResponseWriter, r *http.Request) {
			var args struct {
				Server   MCPServer
				Previous string
			}
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			err := SaveMCP(reveal(args.Server, args.Previous), args.Previous)
			Servers.Forget(args.Server.Name)
			if args.Previous != "" {
				Servers.Forget(args.Previous)
			}
			done(w, err)
		},
		"POST /api/extensions/mcp/remove": func(w http.ResponseWriter, r *http.Request) {
			var args struct{ Name string }
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			Servers.Forget(args.Name)
			done(w, RemoveMCP(args.Name))
		},
		"POST /api/extensions/mcp/status": func(w http.ResponseWriter, r *http.Request) {
			var args struct{ Workspace string }
			if core.ReadJSON(r, &args) != nil {
				core.Fail(w, 400, "Invalid request")
				return
			}
			dir := args.Workspace
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				dir, _ = os.UserHomeDir()
			}
			ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
			defer cancel()
			statuses := Servers.Status(ctx, Enabled().MCP, dir)
			for i := range statuses {
				statuses[i].MCPServer = hide(statuses[i].MCPServer)
			}
			core.WriteJSON(w, 200, map[string]any{"servers": statuses})
		},
	}
}
