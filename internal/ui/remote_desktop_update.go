package ui

// The server's updates, seen from the remote desktop: on connecting, a row
// says when the server runs an older version than the release page's, and
// its button has the server update itself and restart.

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
			if !status.Available {
				remote.serverUpdate.Hide()
				relayout(remote.left)
				return
			}
			if !status.CanUpdate {
				remote.serverUpdate.SetText("Server " + status.Current + " · " + status.Latest + " is out")
				remote.serverUpdate.onTap = func() { remote.status.SetText(status.Problem) }
			} else {
				remote.serverUpdate.SetText("Update server to " + status.Latest)
				remote.serverUpdate.onTap = remote.updateServer
			}
			remote.serverUpdate.Show()
			relayout(remote.left) // Show alone leaves the row at its hidden, empty size
		})
	}()
}

func (remote *remoteDesktop) updateServer() {
	remote.serverUpdate.Disable()
	remote.status.SetText("The server is downloading its update…")
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 30*time.Minute)
		defer cancel()
		var reply struct{ Version string }
		err := remote.client.json(ctx, "POST", "/api/update", map[string]any{}, &reply)
		fyne.Do(func() {
			remote.serverUpdate.Enable()
			if err != nil {
				remote.status.SetText("Server update: " + err.Error())
				return
			}
			remote.serverUpdate.Hide()
			relayout(remote.left)
			remote.status.SetText("The server restarts with " + reply.Version + "; this window reconnects by itself.")
		})
	}()
}
