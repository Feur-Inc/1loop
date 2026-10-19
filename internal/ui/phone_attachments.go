package ui

import (
	"net/http"
	"slices"

	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

func (server *remoteServer) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	workspace := r.URL.Query().Get("workspace")
	allowed := false
	server.onUI(func() {
		if workspace == "" {
			workspace = server.ui.cfg.Workspace
		}
		allowed = workspace == server.ui.cfg.Workspace || slices.Contains(server.ui.projects, workspace)
	})
	if !allowed {
		core.Fail(w, 403, "Choose an existing project")
		return
	}
	remoteapi.UploadAttachment(w, r, workspace)
}
