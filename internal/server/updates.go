package server

// The server's own updates (internal/update): it answers what version it
// runs and whether a newer one is out; a paired device shows that and can
// ask it to update itself and restart. The phone's engine comes with the
// phone app, which updates it.

import (
	"context"
	"net/http"
	"os"
	"time"

	"1loop/internal/core"
	"1loop/internal/platform"
	"1loop/internal/update"
)

// updateStatus is GET /api/update.
func (server *headlessServer) updateStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	status, _ := update.Check(ctx, "server")
	if server.local {
		status.CanUpdate = false
		status.Problem = "This engine comes with the phone app: update the app."
	}
	core.WriteJSON(w, http.StatusOK, status)
}

// installUpdate is POST /api/update: download, check, replace this binary
// and restart in place. The answer comes before the restart.
func (server *headlessServer) installUpdate(w http.ResponseWriter, r *http.Request) {
	if server.local {
		core.Fail(w, http.StatusConflict, "This engine comes with the phone app: update the app.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	status, manifest := update.Check(ctx, "server")
	if !status.CanUpdate {
		problem := status.Problem
		if problem == "" {
			problem = "This server is up to date."
		}
		core.Fail(w, http.StatusConflict, problem)
		return
	}
	exe, err := update.Install(ctx, manifest, "server")
	if err != nil {
		core.Fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]string{"version": manifest.Version})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond) // the answer leaves first
		server.close()                     // chats saved, runs and tasks stopped
		err := platform.RestartProcess(exe)
		os.Stderr.WriteString("1loop server: restart after update failed: " + err.Error() + "\n")
		os.Exit(1)
	}()
}
