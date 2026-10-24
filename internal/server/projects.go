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

func (server *headlessServer) changeProject(w http.ResponseWriter, r *http.Request) {
	var args remoteapi.ProjectChange
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Choose a project folder")
		return
	}
	server.mu.Lock()
	// The server's root is its authorization boundary, not a movable project.
	if !server.local && args.Path == server.root {
		server.mu.Unlock()
		core.Fail(w, 400, "The server's authorized root cannot be renamed or deleted")
		return
	}
	for _, chat := range server.chats {
		if !remoteapi.ProjectWithin(args.Path, chat.Config.Workspace) {
			continue
		}
		busy := chat.run != nil
		for _, task := range chat.tasks.Snapshot() {
			busy = busy || task.Running
		}
		for _, tab := range chat.tabs {
			busy = busy || tab.terminal != nil
		}
		if busy {
			server.mu.Unlock()
			core.Fail(w, 409, "Stop this project's agents and background tasks and close its terminals first")
			return
		}
	}
	destination, err := remoteapi.ChangeProject(server.projects, server.dir, r.PathValue("action"), args)
	if err != nil {
		server.mu.Unlock()
		core.Fail(w, 400, err.Error())
		return
	}
	projects := []string{}
	for _, project := range server.projects {
		if !remoteapi.ProjectWithin(args.Path, project) {
			projects = append(projects, project)
		} else if destination != "" {
			projects = append(projects, remoteapi.MovedProject(args.Path, destination, project))
		}
	}
	server.projects = projects
	kept := []*serverChat{}
	var tabs []*serverTab
	for _, chat := range server.chats {
		if !remoteapi.ProjectWithin(args.Path, chat.Config.Workspace) {
			kept = append(kept, chat)
		} else if destination != "" {
			chat.Config.Workspace = remoteapi.MovedProject(args.Path, destination, chat.Config.Workspace)
			for _, version := range chat.Versions {
				if version.Snapshot != nil {
					version.Snapshot.Config.Workspace = remoteapi.MovedProject(args.Path, destination, version.Snapshot.Config.Workspace)
				}
			}
			kept = append(kept, chat)
		} else {
			tabs = append(tabs, chat.tabs...)
			chat.tabs = nil
		}
	}
	server.chats = kept
	server.cfg.Workspace = remoteapi.MovedProject(args.Path, destination, server.cfg.Workspace)
	if !slices.Contains(projects, server.cfg.Workspace) {
		server.cfg.Workspace = ""
		if len(projects) > 0 {
			server.cfg.Workspace = projects[0]
		}
	}
	err = server.saveSettingsLocked()
	if err == nil {
		err = server.saveLocked()
	}
	reply := map[string]any{"workspace": destination, "previous": args.Path, "projects": projects}
	server.broadcastLocked("projects", reply)
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	server.mu.Unlock()
	for _, tab := range tabs {
		tab.close()
	}
	if err != nil {
		core.Fail(w, 500, "The folder changed, but 1loop could not save the project state: "+err.Error())
		return
	}
	core.WriteJSON(w, 200, reply)
}
