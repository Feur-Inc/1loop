// SPDX-License-Identifier: MPL-2.0

package remoteapi

// The phone API's one HTTP layer (#518): the desktop's remote
// (internal/ui/phone_server.go) and a headless server (internal/server)
// are each an Engine, and NewMux serves it the same way: GET /api/info,
// pairing, the token check, the event stream, and the engine's routes
// behind the token. Harden adds the shared limits and headers, NewServer
// the shared timeouts.

import (
	"errors"
	"net/http"

	"1loop/internal/core"
)

// Engine is what serves the phone API: the desktop or a headless server.
type Engine interface {
	// Info is GET /api/info, served without a token.
	Info() map[string]any
	// Allows tells whether a token's hash is a paired device's. It must not
	// wait for a busy window or engine.
	Allows(tokenHash string) bool
	// RequestPairing and ConfirmPairing are POST /api/pair and
	// /api/pair/confirm, with the shared PairingBook. An error is a
	// StatusError, or the default status of its route.
	RequestPairing(r *http.Request, args PairRequest) (map[string]any, error)
	ConfirmPairing(r *http.Request, args PairConfirm) (map[string]any, error)
	// Events are GET /api/events.
	Events() *Events
	// Routes are the engine's own routes ("METHOD /path"), served to paired
	// devices only.
	Routes() map[string]http.HandlerFunc
}

// PairRequest is POST /api/pair's body.
type PairRequest struct {
	Device string `json:"device"`
	Setup  string `json:"setup"` // a headless server's one-time setup link
}

// PairConfirm is POST /api/pair/confirm's body.
type PairConfirm struct {
	Pairing string `json:"pairing"`
	Code    string `json:"code"`
	Device  string `json:"device"` // the name a QR pairing takes
}

// StatusError is an error with the HTTP status it answers.
type StatusError struct {
	Status int
	Err    error
}

func (err StatusError) Error() string { return err.Err.Error() }
func (err StatusError) Unwrap() error { return err.Err }

// fail answers err with its status, or fallback.
func fail(w http.ResponseWriter, err error, fallback int) {
	var status StatusError
	if errors.As(err, &status) {
		fallback = status.Status
	}
	core.Fail(w, fallback, err.Error())
}

// Unpaired is the answer to a request without a paired device's token.
const Unpaired = "This device is not paired with 1loop"

// Authorized lets a request through only with a paired device's token.
func Authorized(engine Engine, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !engine.Allows(RequestTokenHash(r)) {
			core.Fail(w, http.StatusUnauthorized, Unpaired)
			return
		}
		next(w, r)
	}
}

// PairHandler is POST /api/pair.
func PairHandler(engine Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var args PairRequest
		if core.ReadJSON(r, &args) != nil {
			core.Fail(w, http.StatusBadRequest, "Invalid request")
			return
		}
		reply, err := engine.RequestPairing(r, args)
		if err != nil {
			fail(w, err, http.StatusTooManyRequests)
			return
		}
		core.WriteJSON(w, http.StatusOK, reply)
	}
}

// ConfirmHandler is POST /api/pair/confirm: a pairing still waiting for
// approval answers 409, which the phone app polls on.
func ConfirmHandler(engine Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var args PairConfirm
		if core.ReadJSON(r, &args) != nil {
			core.Fail(w, http.StatusBadRequest, "Invalid request")
			return
		}
		reply, err := engine.ConfirmPairing(r, args)
		switch {
		case errors.Is(err, ErrPairingNotApproved):
			fail(w, err, http.StatusConflict)
		case err != nil:
			fail(w, err, http.StatusForbidden)
		default:
			core.WriteJSON(w, http.StatusOK, reply)
		}
	}
}

// NewMux is the engine's phone API: GET /api/info, pairing when pairing
// is true (a phone's own engine pairs only on its sharing listener), and
// GET /api/events with the engine's routes behind a paired device's token.
// The caller may add its own public routes, then wraps it in Harden.
func NewMux(engine Engine, pairing bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, _ *http.Request) {
		core.WriteJSON(w, http.StatusOK, engine.Info())
	})
	if pairing {
		mux.HandleFunc("POST /api/pair", PairHandler(engine))
		mux.HandleFunc("POST /api/pair/confirm", ConfirmHandler(engine))
	}
	mux.HandleFunc("GET /api/events", Authorized(engine, engine.Events().Serve))
	for path, handler := range engine.Routes() {
		mux.HandleFunc(path, Authorized(engine, handler))
	}
	return mux
}
