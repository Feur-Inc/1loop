package ui

import (
	"1loop/internal/core"
	"1loop/internal/remoteapi"
	"fmt"
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
	var err error
	var reply map[string]any
	server.onUI(func() { reply, err = server.ui.changeProject(r.PathValue("action"), args) })
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, reply)
}

func (ui *desktopUI) changeProject(action string, args remoteapi.ProjectChange) (map[string]any, error) {
	ui.panel.stash()
	panels := []*panelState{&ui.panel.home}
	for _, chat := range ui.chats {
		if chat.panel != nil {
			panels = append(panels, chat.panel)
		}
	}
	for _, panel := range panels {
		for _, tab := range panel.tabs {
			if tab.kind == "terminal" && remoteapi.ProjectWithin(args.Path, tab.workspace) {
				return nil, fmt.Errorf("Close this project's terminals first")
			}
		}
	}
	for _, chat := range ui.chats {
		if !remoteapi.ProjectWithin(args.Path, chat.Config.Workspace) {
			continue
		}
		busy := chat.run != nil
		for _, task := range chat.tasks.Snapshot() {
			busy = busy || task.Running
		}
		if busy {
			return nil, fmt.Errorf("Stop this project's agents and background tasks and close its terminals first")
		}
	}
	destination, err := remoteapi.ChangeProject(ui.projects, filepath.Dir(ui.statePath), action, args)
	if err != nil {
		return nil, err
	}
	projects := []string{}
	for _, project := range ui.projects {
		if !remoteapi.ProjectWithin(args.Path, project) {
			projects = append(projects, project)
		} else if destination != "" {
			projects = append(projects, remoteapi.MovedProject(args.Path, destination, project))
		}
	}
	if destination != "" {
		for _, project := range ui.projects {
			if remoteapi.ProjectWithin(args.Path, project) {
				ui.prompt.drafts.move(newChatDraft+project, newChatDraft+remoteapi.MovedProject(args.Path, destination, project))
			}
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
			delete(ui.remote.dirty, chat)
			delete(ui.remote.meta, chat)
		}
	}
	ui.cfg.Workspace = remoteapi.MovedProject(args.Path, destination, ui.cfg.Workspace)
	if !slices.Contains(projects, ui.cfg.Workspace) {
		ui.cfg.Workspace = ""
		if len(projects) > 0 {
			ui.cfg.Workspace = projects[0]
		}
	}
	if destination != "" {
		// File and diff tabs capture their root in callbacks; rebuild them for the renamed folder.
		for _, panel := range panels {
			for index, tab := range panel.tabs {
				if !remoteapi.ProjectWithin(args.Path, tab.workspace) {
					continue
				}
				root := remoteapi.MovedProject(args.Path, destination, tab.workspace)
				switch tab.kind {
				case "files":
					tab.dispose()
					panel.tabs[index] = ui.panel.filesTab(root)
				case "changes":
					tab.dispose()
					panel.tabs[index] = ui.panel.changesTab(root)
				}
			}
		}
		ui.panel.render()
	}
	if destination != "" && ui.current == nil {
		ui.prompt.renameDraft(ui.draftKey())
	}
	ui.updateHeader()
	ui.saveNow()
	ui.renderChats()
	reply := map[string]any{"workspace": destination, "previous": args.Path, "projects": projects}
	if ui.remote != nil {
		ui.remote.broadcast("projects", reply)
	}
	return reply, nil
}
