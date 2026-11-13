// SPDX-License-Identifier: MPL-2.0

package server

// Install Chromium and Install Claude Code (issue #406): a server without
// them (the slim Docker image) downloads them into its data folder when a
// paired client asks, without administrator rights, so they survive
// restarts and new images. The download runs in the background; the
// clients follow it in /api/state's "installs" and the "install" event.

import (
	"context"
	"net/http"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
)

// installJob is how an install goes: running, what it is doing, or why it
// failed.
type installJob struct {
	Running bool   `json:"running"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
}

// installers are what each install does (variables for tests).
var installers = map[string]func(ctx context.Context, progress func(string)) error{
	"chromium": func(ctx context.Context, progress func(string)) error {
		_, err := browser.InstallChromium(ctx, progress)
		return err
	},
	"claude-code": func(ctx context.Context, progress func(string)) error {
		_, err := agent.InstallClaudeCodeIn(ctx, agent.ClaudeInstallDir, progress)
		return err
	},
}

// installed says whether what an install would add is there already.
func (server *headlessServer) installed(what string) bool {
	if what == "chromium" {
		return server.hasBrowser()
	}
	return hasClaudeCode()
}

// install starts an install: POST /api/install/chromium or
// /api/install/claude-code. It answers at once with the job, which goes on
// after the request.
func (server *headlessServer) install(w http.ResponseWriter, r *http.Request) {
	what := r.PathValue("what")
	run, ok := installers[what]
	if !ok {
		core.Fail(w, 404, "Nothing to install by that name")
		return
	}
	if server.installed(what) {
		core.WriteJSON(w, 200, map[string]any{"installed": true})
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if job := server.installs[what]; job != nil && job.Running {
		core.WriteJSON(w, 202, map[string]any{"job": *job})
		return
	}
	if server.installs == nil {
		server.installs = map[string]*installJob{}
	}
	job := &installJob{Running: true, Text: "Starting…"}
	server.installs[what] = job
	server.broadcastInstallLocked(what)
	go func() {
		err := run(server.ctx, func(text string) {
			server.mu.Lock()
			job.Text = text
			server.broadcastInstallLocked(what)
			server.mu.Unlock()
		})
		server.mu.Lock()
		defer server.mu.Unlock()
		job.Running, job.Text = false, ""
		if err != nil {
			job.Error = err.Error()
		}
		server.broadcastInstallLocked(what)
	}()
	core.WriteJSON(w, 202, map[string]any{"job": *job})
}

// installsLocked copies the jobs for /api/state.
func (server *headlessServer) installsLocked() map[string]installJob {
	jobs := map[string]installJob{}
	for what, job := range server.installs {
		jobs[what] = *job
	}
	return jobs
}

// broadcastInstallLocked tells the clients how an install goes; once it is
// over, what the server now has.
func (server *headlessServer) broadcastInstallLocked(what string) {
	job := *server.installs[what]
	event := map[string]any{"what": what, "job": job}
	if !job.Running {
		// Cheap enough under the lock: they only look for files.
		event["browser"], event["claudeCode"] = server.hasBrowser(), hasClaudeCode()
	}
	server.broadcastLocked("install", event)
}
