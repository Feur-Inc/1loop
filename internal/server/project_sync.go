package server

// Copying a project with its chats between this engine and a paired one
// (projectsync): the routes peers call, and, on the phone, the copy its app
// asks for with a desktop or a server.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/projectsync"
	"1loop/internal/remoteapi"
)

func (server *headlessServer) syncEngine() *projectsync.Local {
	return &projectsync.Local{
		Device:   server.name,
		Identity: server.fingerprint,
		Bases:    &projectsync.BaseStore{Path: filepath.Join(server.dir, "sync-bases.json")},
		Resolve: func(project string, create bool) (string, error) {
			server.mu.Lock()
			var found string
			for _, folder := range server.projects {
				if folder == project {
					found = folder
					break
				}
			}
			if found == "" && project == filepath.Base(project) {
				for _, folder := range server.projects {
					if filepath.Base(folder) == project {
						if found != "" {
							server.mu.Unlock()
							return "", fmt.Errorf("More than one project has this name: select its full folder")
						}
						found = folder
					}
				}
			}
			if found != "" {
				for _, chat := range server.chats {
					if chat.Config.Workspace == found && chat.run != nil {
						server.mu.Unlock()
						return "", fmt.Errorf("Stop the running project conversations before copying")
					}
				}
				server.mu.Unlock()
				return found, nil
			}
			server.mu.Unlock()
			if !create {
				return "", fmt.Errorf("No project %q here: %w", project, os.ErrNotExist)
			}

			name := filepath.Base(filepath.Clean(project))
			if name == "." || name == "/" || strings.HasPrefix(name, ".") {
				return "", fmt.Errorf("Invalid project name")
			}
			folder, err := remoteapi.ProjectFolder(server.projectBase(), name, false)
			if err != nil {
				if folder, err = remoteapi.ProjectFolder(server.projectBase(), name, true); err != nil {
					return "", err
				}
			}
			if server.local {
				os.MkdirAll(filepath.Join(folder, ".home"), 0755)
			}
			server.mu.Lock()
			defer server.mu.Unlock()
			if !slices.Contains(server.projects, folder) {
				server.projects = append(server.projects, folder)
				server.saveSettingsLocked()
				server.broadcastLocked("projects", map[string]any{"projects": server.projects})
			}
			return folder, nil
		},
		Chats: func(workspace string) []projectsync.Chat {
			server.mu.Lock()
			defer server.mu.Unlock()
			var out []projectsync.Chat
			for _, chat := range server.chats {
				if chat.Config.Workspace == workspace {
					out = append(out, (projectsync.Chat{ID: chat.ID, Title: chat.Title, Config: chat.Config, History: chat.History, Items: chat.Items, Todo: chat.Todo, Version: chat.Version, Versions: chat.Versions}).Portable())
				}
			}
			return out
		},
		AddChat: func(workspace string, copied projectsync.Chat, conflict bool) (string, error) {
			server.mu.Lock()
			defer server.mu.Unlock()
			existing := server.chatLocked(copied.ID)
			if conflict || existing != nil && existing.Config.Workspace != workspace {
				copied.ID = core.Identifier()[:16]
				copied.Title += " (copy)"
				existing = nil
			}
			if existing != nil {
				if existing.run != nil {
					return "", fmt.Errorf("“%s” is running here: stop it before copying", existing.Title)
				}
				// The chat goes on here with this engine's model; a Claude Code
				// session here reads what happened elsewhere when it resumes.
				cfg := existing.Config
				cfg.RanWith = ranWith(copied.Config)
				copied = copied.AtWorkspace(cfg)
				existing.Config.Session, existing.Config.SessionMark = "", 0
				existing.Cache = &agent.ContextCache{}
				existing.Title, existing.History, existing.Items = copied.Title, copied.History, copied.Items
				existing.Todo, existing.Version, existing.Versions = copied.Todo, copied.Version, copied.Versions
				existing.Config.RanWith = ranWith(copied.Config)
				server.broadcastLocked("replace", map[string]string{"chat": existing.ID})
			} else {
				cfg := server.cfg
				cfg.Workspace, cfg.Session, cfg.SessionMark = workspace, "", 0
				cfg.RanWith = ranWith(copied.Config) // its next turn here goes through a handoff
				copied = copied.AtWorkspace(cfg)
				if copied.Todo == nil {
					copied.Todo = &agent.TodoState{}
				}
				server.chats = append([]*serverChat{{ID: copied.ID, Title: copied.Title, Config: cfg, History: copied.History, Items: copied.Items,
					Todo: copied.Todo, Version: copied.Version, Versions: copied.Versions, Cache: &agent.ContextCache{}}}, server.chats...)
			}
			return copied.Title, nil
		},
		Saved: func() error {
			server.mu.Lock()
			defer server.mu.Unlock()
			if err := server.saveLocked(); err != nil {
				return err
			}
			server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
			return nil
		},
	}
}

// ranWith is who a copied chat last ran with, for the handoff here.
func ranWith(cfg agent.Config) *agent.RunIdentity {
	if cfg.RanWith != nil {
		identity := *cfg.RanWith
		return &identity
	}
	if cfg.Provider == "" {
		return nil
	}
	identity := agent.IdentityOf(cfg)
	return &identity
}

// localSync is POST /api/local/sync, the phone app's: copy a project
// between this phone and a paired desktop or server, either way, or only
// say what a copy would do.
func (server *headlessServer) localSync(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Peer      projectsync.Peer
		Direction string // "pull" (to this phone) or "push"
		Local     string // the project here (a folder or a name)
		Remote    string // the project there
		Plan      bool   // only say what it would do
	}
	if core.ReadJSON(r, &args) != nil || args.Peer.URL == "" || args.Peer.Token == "" || args.Direction != "push" && args.Direction != "pull" {
		core.Fail(w, 400, "Invalid copy request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
	defer cancel()
	here := server.syncEngine()
	bases := &projectsync.BaseStore{Path: filepath.Join(server.dir, "sync-bases.json")}
	var report projectsync.Report
	var err error
	if args.Direction == "push" {
		report, err = projectsync.Copy(ctx, here, args.Local, &args.Peer, args.Remote, bases, args.Plan)
	} else {
		report, err = projectsync.Copy(ctx, &args.Peer, args.Remote, here, args.Local, bases, args.Plan)
	}
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]any{"report": report, "summary": report.Summary()})
}
