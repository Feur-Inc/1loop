package githubconnector

import (
	"net/http"

	"1loop/internal/core"
	"1loop/internal/extensions"
)

// Handlers are installed behind the execution target's existing pairing
// authorization, like its model-account and extension settings.
func Handlers() map[string]http.HandlerFunc {
	answer := func(w http.ResponseWriter, err error) {
		if err != nil {
			core.Fail(w, http.StatusBadRequest, err.Error())
			return
		}
		core.WriteJSON(w, http.StatusOK, Default.Status())
	}
	return map[string]http.HandlerFunc{
		"GET /api/github": func(w http.ResponseWriter, r *http.Request) {
			answer(w, nil)
		},
		"POST /api/github/configure": func(w http.ResponseWriter, r *http.Request) {
			var body struct{ ClientID string }
			if core.ReadJSON(r, &body) != nil {
				core.Fail(w, http.StatusBadRequest, "Invalid GitHub App configuration")
				return
			}
			answer(w, Default.Configure(body.ClientID))
		},
		"POST /api/github/connect": func(w http.ResponseWriter, r *http.Request) {
			answer(w, Default.Start(r.Context()))
		},
		"POST /api/github/cancel": func(w http.ResponseWriter, r *http.Request) {
			Default.Cancel()
			answer(w, nil)
		},
		"POST /api/github/enable": func(w http.ResponseWriter, r *http.Request) {
			var body struct{ Enabled bool }
			if core.ReadJSON(r, &body) != nil {
				core.Fail(w, http.StatusBadRequest, "Invalid GitHub connector state")
				return
			}
			err := Default.Enable(body.Enabled)
			if err == nil && !body.Enabled {
				extensions.Servers.Forget(ServerName)
			}
			answer(w, err)
		},
		"POST /api/github/attachment-token": func(w http.ResponseWriter, r *http.Request) {
			var body struct{ Token string }
			if core.ReadJSON(r, &body) != nil {
				core.Fail(w, http.StatusBadRequest, "Invalid GitHub attachment token")
				return
			}
			answer(w, Default.SetAttachmentToken(r.Context(), body.Token))
		},
		"POST /api/github/disconnect": func(w http.ResponseWriter, r *http.Request) {
			err := Default.Disconnect()
			if err == nil {
				extensions.Servers.Forget(ServerName)
			}
			answer(w, err)
		},
	}
}
