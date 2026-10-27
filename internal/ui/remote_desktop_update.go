package ui

// The server's updates, seen from the desktop: on connecting, the target
// menu offers to update a server that runs an older version than the
// release page's; the server then updates itself and restarts.

import (
	"context"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/update"
)

func (remote *remoteDesktop) checkServerUpdate() {
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 40*time.Second)
		defer cancel()
		var status update.Status
		if err := remote.client.json(ctx, "GET", "/api/update", nil, &status); err != nil {
			return // an older server without updates
		}
		fyne.Do(func() {
			if status.Available {
				remote.update = &status
				remote.status.SetText("Server " + status.Current + " · " + status.Latest + " is out: see the target menu")
			}
		})
	}()
}

func (remote *remoteDesktop) updateServer() {
	remote.status.SetText("The server is downloading its update…")
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 30*time.Minute)
		defer cancel()
		var reply struct{ Version string }
		err := remote.client.json(ctx, "POST", "/api/update", map[string]any{}, &reply)
		fyne.Do(func() {
			if err != nil {
				remote.status.SetText("Server update: " + err.Error())
				return
			}
			remote.update = nil
			remote.status.SetText("The server restarts with " + reply.Version + "; 1loop reconnects by itself.")
		})
	}()
}
