package ui

import (
	"1loop/internal/core"
	"1loop/internal/remoteapi"
	"net/http"
	"path/filepath"
	"slices"
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

func (server *remoteServer) changeProject(w http.ResponseWriter, r *http.Request) {
	var args remoteapi.ProjectChange
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Choose a project folder")
		return
	}
	var problem string
	var reply map[string]any
	server.onUI(func() {
		ui := server.ui
		for _, chat := range ui.chats {
			if !remoteapi.ProjectWithin(args.Path, chat.Config.Workspace) {
				continue
			}
			busy := chat.run != nil
			for _, task := range chat.tasks.Snapshot() {
				busy = busy || task.Running
			}
			if chat.panel != nil {
				for _, tab := range chat.panel.tabs {
					busy = busy || tab.kind == "terminal"
				}
			}
			if busy {
				problem = "Stop this project's agents and background tasks and close its terminals first"
				return
			}
		}
		destination, err := remoteapi.ChangeProject(ui.projects, filepath.Dir(ui.statePath), r.PathValue("action"), args)
		if err != nil {
			problem = err.Error()
			return
		}
		projects := []string{}
		for _, project := range ui.projects {
			if !remoteapi.ProjectWithin(args.Path, project) {
				projects = append(projects, project)
			} else if destination != "" {
				projects = append(projects, remoteapi.MovedProject(args.Path, destination, project))
			}
		}
		ui.projects = projects
		// Copy before removing chats: removeChat updates the live slice and panel.
		for _, chat := range append([]*conversation{}, ui.chats...) {
			if !remoteapi.ProjectWithin(args.Path, chat.Config.Workspace) {
				continue
			}
			if destination != "" {
				chat.Config.Workspace = remoteapi.MovedProject(args.Path, destination, chat.Config.Workspace)
				for _, version := range chat.Versions {
					if version.Snapshot != nil {
						version.Snapshot.Config.Workspace = remoteapi.MovedProject(args.Path, destination, version.Snapshot.Config.Workspace)
					}
				}
			} else {
				ui.removeChat(chat)
				delete(server.dirty, chat)
				delete(server.meta, chat)
			}
		}
		ui.cfg.Workspace = remoteapi.MovedProject(args.Path, destination, ui.cfg.Workspace)
		if !slices.Contains(projects, ui.cfg.Workspace) {
			ui.cfg.Workspace = ""
			if len(projects) > 0 {
				ui.cfg.Workspace = projects[0]
			}
		}
		ui.saveNow()
		ui.renderChats()
		server.chatsChanged()
		reply = map[string]any{"workspace": destination, "previous": args.Path, "projects": projects}
	})
	if problem != "" {
		core.Fail(w, 400, problem)
		return
	}
	server.broadcast("projects", reply)
	core.WriteJSON(w, 200, reply)
}
