package extensions

// The extensions' routes, the same on a server, on the phone's own engine
// and on the desktop the phone drives.

import (
	"context"
	"net/http"
	"os"
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
	Dir     string       `json:"dir"`
}

// View reads what is installed, for the settings.
func View() (Installed, error) {
	registry, err := Load()
	if err != nil {
		return Installed{}, err
	}
	view := Installed{Skills: registry.Skills, Plugins: []PluginView{}, MCP: registry.MCP, Dir: Dir()}
	if view.Skills == nil {
		view.Skills = []Skill{}
	}
	if view.MCP == nil {
		view.MCP = []MCPServer{}
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
		core.WriteJSON(w, 200, view)
	}
	return map[string]http.HandlerFunc{
		"GET /api/extensions": func(w http.ResponseWriter, r *http.Request) {
			view, err := View()
			if err != nil {
				core.Fail(w, 500, err.Error())
				return
			}
			core.WriteJSON(w, 200, view)
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
			err := SaveMCP(args.Server, args.Previous)
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
			core.WriteJSON(w, 200, map[string]any{"servers": Servers.Status(ctx, Enabled().MCP, dir)})
		},
	}
}
