// SPDX-License-Identifier: MPL-2.0

package server

// A project's environment variables (#408), kept in the server's
// config.json beside its projects: its chats' commands and terminals get
// them, here or on the project's SSH machine. Paired clients read and
// replace them with GET and POST /api/projects/env; they are never logged,
// broadcast with the projects, nor put in a prompt.

import (
	"maps"
	"net/http"
	"slices"

	"1loop/internal/core"
	"1loop/internal/platform"
	"1loop/internal/remoteapi"
)

// projectEnvLocked is the variables of the project a folder is, or the
// project a worktree folder belongs to.
func (server *headlessServer) projectEnvLocked(folder string) map[string]string {
	if project := server.worktrees[folder]; project != "" {
		folder = project
	}
	return maps.Clone(server.projectEnv[folder])
}

// projectEnvPath is the registered project a request names, or "" after
// refusing it.
func (server *headlessServer) projectEnvPath(w http.ResponseWriter, path string) string {
	if !slices.Contains(server.projects, path) {
		core.Fail(w, 400, "Choose a registered project")
		return ""
	}
	return path
}

// readProjectEnv is GET /api/projects/env?path=.
func (server *headlessServer) readProjectEnv(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	path := server.projectEnvPath(w, r.URL.Query().Get("path"))
	if path == "" {
		return
	}
	env := server.projectEnvLocked(path)
	if env == nil {
		env = map[string]string{}
	}
	core.WriteJSON(w, 200, remoteapi.ProjectEnv{Path: path, Env: env})
}

// saveProjectEnv is POST /api/projects/env with {path, env}: env replaces
// the project's variables; an empty one forgets them.
func (server *headlessServer) saveProjectEnv(w http.ResponseWriter, r *http.Request) {
	var args remoteapi.ProjectEnv
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Send the project and its variables")
		return
	}
	for name := range args.Env {
		if !platform.ValidEnvName(name) {
			core.Fail(w, 400, "Not a variable name: "+name)
			return
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	path := server.projectEnvPath(w, args.Path)
	if path == "" {
		return
	}
	previous, had := server.projectEnv[path]
	server.setProjectEnvLocked(path, args.Env)
	if err := server.saveSettingsLocked(); err != nil {
		if had {
			server.projectEnv[path] = previous
		} else {
			delete(server.projectEnv, path)
		}
		core.Fail(w, 500, "Unable to save the variables")
		return
	}
	core.WriteJSON(w, 200, remoteapi.ProjectEnv{Path: path, Env: map[string]string{}})
}

// setProjectEnvLocked keeps a project's variables; none forgets them.
func (server *headlessServer) setProjectEnvLocked(project string, env map[string]string) {
	if len(env) == 0 {
		delete(server.projectEnv, project)
		return
	}
	if server.projectEnv == nil {
		server.projectEnv = map[string]map[string]string{}
	}
	server.projectEnv[project] = maps.Clone(env)
}

// moveProjectEnvLocked follows projects renamed (destination) or deleted
// ("") under path.
func (server *headlessServer) moveProjectEnvLocked(path, destination string) {
	for project, env := range server.projectEnv {
		if !remoteapi.ProjectWithin(path, project) {
			continue
		}
		delete(server.projectEnv, project)
		if destination != "" {
			server.projectEnv[remoteapi.MovedProject(path, destination, project)] = env
		}
	}
}
