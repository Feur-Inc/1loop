package ui

import (
	"1loop/internal/core"
	"1loop/internal/remoteapi"
	"net/http"
	"strings"
)

func (server *remoteServer) setPreferences(w http.ResponseWriter, r *http.Request) {
	var args struct{ UserPreferences string }
	if core.ReadJSON(r, &args) != nil || len(args.UserPreferences) > 65536 {
		core.Fail(w, 400, "Preferences must be at most 64 KiB")
		return
	}
	server.onUI(func() {
		server.ui.userInstructions = strings.TrimSpace(args.UserPreferences)
		server.ui.save()
		server.settingsChanged()
	})
	core.WriteJSON(w, 200, map[string]string{"userPreferences": strings.TrimSpace(args.UserPreferences)})
}

func (server *remoteServer) projectFolders(w http.ResponseWriter, r *http.Request) {
	var roots []string
	var path string
	server.onUI(func() { roots = append(roots, server.ui.projects...); path = server.ui.cfg.Workspace })
	if requested := r.URL.Query().Get("path"); requested != "" {
		path = requested
	}
	for _, root := range roots {
		folders, err := remoteapi.ProjectFolders(root, path)
		if err == nil {
			core.WriteJSON(w, 200, folders)
			return
		}
	}
	core.Fail(w, 403, "Choose a folder within a project authorized on the desktop")
}

func (server *remoteServer) addProject(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Path   string
		Create bool
	}
	if core.ReadJSON(r, &args) != nil || args.Path == "" {
		core.Fail(w, 400, "Choose a project folder")
		return
	}
	var roots []string
	server.onUI(func() { roots = append(roots, server.ui.projects...) })
	for _, root := range roots {
		folder, err := remoteapi.ProjectFolder(root, args.Path, args.Create)
		if err != nil {
			continue
		}
		var projects []string
		server.onUI(func() {
			server.ui.ensureProject(folder)
			server.ui.renderChats()
			server.ui.save()
			projects = append(projects, server.ui.projects...)
		})
		server.broadcast("projects", map[string]any{"projects": projects})
		core.WriteJSON(w, 200, map[string]any{"workspace": folder, "projects": projects})
		return
	}
	core.Fail(w, 403, "Choose a folder within a project authorized on the desktop")
}
