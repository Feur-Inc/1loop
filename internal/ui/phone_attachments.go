package ui

import (
	"net/http"
	"slices"

	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

func (server *remoteServer) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	server.projectFiles(remoteapi.UploadAttachment)(w, r)
}

// projectFiles runs handler in one of the desktop's projects.
func (server *remoteServer) projectFiles(handler func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		server.inProject(w, r, handler)
	}
}

func (server *remoteServer) inProject(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request, string)) {
	workspace := r.URL.Query().Get("workspace")
	allowed := false
	server.onUI(func() {
		if workspace == "" {
			workspace = server.ui.cfg.Workspace
		}
		allowed = workspace == server.ui.cfg.Workspace || slices.Contains(server.ui.projects, workspace) || server.ui.worktrees[workspace] != ""
	})
	if !allowed {
		core.Fail(w, 403, "Choose an existing project")
		return
	}
	handler(w, r, workspace)
}
