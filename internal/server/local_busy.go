package server

import (
	"net/http"

	"1loop/internal/core"
)

// localBusy tells the phone whether its engine still works for the user: a
// turn, a message waiting for one, a background command or an open
// terminal. In the background, the app stops an engine that stays idle.
func (server *headlessServer) localBusy(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	busy := false
	for _, chat := range server.panelContextsLocked() {
		if chat.run != nil || len(chat.queue) > 0 || len(chat.steer) > 0 {
			busy = true
		}
		for _, task := range chat.tasks.Snapshot() {
			busy = busy || task.Running
		}
		for _, tab := range chat.tabs {
			if tab.terminal != nil {
				select {
				case <-tab.terminal.done:
				default:
					busy = true // its programs end with the engine
				}
			}
		}
	}
	core.WriteJSON(w, 200, map[string]bool{"busy": busy})
}
