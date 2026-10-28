package server

import (
	"fmt"
	"slices"

	"1loop/internal/remoteapi"
)

// gitTarget serves the repositories of the server's projects
// (internal/gitrepo). A worktree's chats belong to its project.
type gitTarget struct{ server *headlessServer }

func (target gitTarget) Project(path string) (string, error) {
	server := target.server
	server.mu.Lock()
	registered := slices.Contains(server.projects, path)
	server.mu.Unlock()
	if !registered {
		return "", fmt.Errorf("Choose a registered project")
	}
	return remoteapi.ProjectFolder(server.projectBase(), path, false)
}

func (target gitTarget) Opened(project, worktree string) {
	server := target.server
	// Only a worktree inside the projects folder can be worked in.
	if _, err := remoteapi.ProjectFolder(server.projectBase(), worktree, false); err != nil {
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.worktrees[worktree] == project {
		return
	}
	if server.worktrees == nil {
		server.worktrees = map[string]string{}
	}
	server.worktrees[worktree] = project
	server.projects = slices.DeleteFunc(server.projects, func(path string) bool { return path == worktree })
	_ = server.saveSettingsLocked()
	server.broadcastLocked("projects", map[string]any{"projects": server.projects, "worktrees": server.worktrees})
}

// Removed forgets a removed worktree and its chats.
func (target gitTarget) Removed(project, worktree string) {
	server := target.server
	server.mu.Lock()
	delete(server.worktrees, worktree)
	var gone []*serverChat
	kept := server.chats[:0:0]
	for _, chat := range server.chats {
		if chat.Config.Workspace == worktree {
			gone = append(gone, chat)
		} else {
			kept = append(kept, chat)
		}
	}
	server.chats = kept
	_ = server.saveLocked()
	_ = server.saveSettingsLocked()
	var tabs []*serverTab
	for _, chat := range gone {
		if chat.run != nil {
			chat.run.cancel()
			chat.run = nil
		}
		chat.queue, chat.steer = nil, nil
		chat.tasks.StopAll()
		tabs = append(tabs, chat.tabs...)
		chat.tabs = nil
	}
	server.broadcastLocked("chats", map[string]any{"chats": server.summariesLocked()})
	server.broadcastLocked("projects", map[string]any{"projects": server.projects, "worktrees": server.worktrees})
	server.mu.Unlock()
	for _, tab := range tabs {
		tab.close()
	}
}

// worktreeLocked says whether path is a known worktree of a registered project.
func (server *headlessServer) worktreeLocked(path string) bool {
	project, ok := server.worktrees[path]
	return ok && slices.Contains(server.projects, project)
}
