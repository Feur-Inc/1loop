package server

import (
	"1loop/internal/core"
	"1loop/internal/remoteapi"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
)

func (server *headlessServer) projectBase() string {
	if server.local {
		return filepath.Dir(server.root)
	}
	return server.root
}

func (server *headlessServer) workspaceLocked(requested string) (string, error) {
	if requested == "" {
		requested = server.root
	}
	if !slices.Contains(server.projects, requested) {
		return "", fmt.Errorf("Choose a registered project")
	}
	return remoteapi.ProjectFolder(server.projectBase(), requested, false)
}

func (server *headlessServer) projectFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := remoteapi.ProjectFolders(server.projectBase(), r.URL.Query().Get("path"))
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, folders)
}

func (server *headlessServer) addProject(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Path   string
		Create bool
	}
	if core.ReadJSON(r, &args) != nil || args.Path == "" {
		core.Fail(w, 400, "Choose a project folder")
		return
	}
	if server.local && filepath.Clean(args.Path) == server.projectBase() {
		core.Fail(w, 400, "Choose a project inside the projects folder")
		return
	}
	folder, err := remoteapi.ProjectFolder(server.projectBase(), args.Path, args.Create)
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	if server.local {
		if err := os.MkdirAll(filepath.Join(folder, ".home"), 0755); err != nil {
			core.Fail(w, 500, "Unable to prepare the project home")
			return
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if !slices.Contains(server.projects, folder) {
		server.projects = append(server.projects, folder)
		if err := server.saveSettingsLocked(); err != nil {
			server.projects = server.projects[:len(server.projects)-1]
			core.Fail(w, 500, "Unable to save the project")
			return
		}
	}
	server.broadcastLocked("projects", map[string]any{"projects": server.projects})
	core.WriteJSON(w, 200, map[string]any{"workspace": folder, "projects": server.projects})
}
