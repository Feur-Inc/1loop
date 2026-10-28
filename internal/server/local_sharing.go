package server

// Sharing the phone's engine on the network: the phone's own app turns it
// on (it is off at every start), and the desktop or another phone then
// connects to it as to any headless server. They pair as with the console:
// the device shows a code, and the phone's user types it to approve.
// /api/local/* stays the phone app's alone.

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

// sharingPort is where other devices reach the phone, as a desktop's phone
// remote (another port when it is taken).
const sharingPort = 47101

// phoneApp says whether a request carries the phone app's own token.
func (server *headlessServer) phoneApp(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return server.localHash != "" && token != r.Header.Get("Authorization") &&
		subtle.ConstantTimeCompare([]byte(remoteapi.TokenHash(token)), []byte(server.localHash)) == 1
}

// localOnly lets a request through only with the phone app's own token.
func (server *headlessServer) localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !server.phoneApp(r) {
			core.Fail(w, http.StatusForbidden, "Only this phone's app can do this")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

// networkRoutes are what other devices reach: pairing, and the chats behind
// it; never the phone app's own /api/local/*.
func (server *headlessServer) networkRoutes(local http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/pair", server.pair)
	mux.HandleFunc("POST /api/pair/confirm", server.confirmPair)
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		core.WriteJSON(w, 200, map[string]any{"name": server.name, "protocol": 1, "headless": true, "secure": true, "consolePairing": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/local/"):
			core.Fail(w, http.StatusForbidden, "Not available over the network")
		case r.URL.Path == "/api/pair" || r.URL.Path == "/api/pair/confirm" || r.URL.Path == "/api/info":
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			mux.ServeHTTP(w, r)
		default:
			local.ServeHTTP(w, r)
		}
	})
}

// sharingState is GET /api/local/sharing: whether the phone is shared,
// where, its certificate, the paired devices and the pairings waiting.
func (server *headlessServer) sharingState(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	devices := []map[string]any{}
	for _, device := range server.devices {
		devices = append(devices, map[string]any{"name": device.Name, "added": device.Added})
	}
	pending := []map[string]any{}
	for id, pair := range server.pairs {
		if time.Now().Before(pair.Expires) && !pair.Approved && pair.Tries < 5 {
			pending = append(pending, map[string]any{"id": id, "device": pair.Device, "expiresAt": pair.Expires.UnixMilli()})
		}
	}
	port := 0
	if server.sharing != nil {
		port = server.sharingPort
	}
	core.WriteJSON(w, 200, map[string]any{"enabled": server.sharing != nil, "port": port, "fingerprint": server.fingerprint, "devices": devices, "pending": pending})
}

// setSharing is POST /api/local/sharing {enabled}: the network listener
// opens or closes. Closing it also ends the network devices' streams.
func (server *headlessServer) setSharing(w http.ResponseWriter, r *http.Request) {
	var args struct{ Enabled bool }
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if args.Enabled && server.sharing == nil {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", sharingPort))
		if err != nil {
			listener, err = net.Listen("tcp", ":0")
		}
		if err != nil {
			core.Fail(w, 500, "Cannot listen on the network: "+err.Error())
			return
		}
		server.sharingPort = listener.Addr().(*net.TCPAddr).Port
		shared := &http.Server{
			Handler:           server.networkRoutes(server.handler),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
			MaxHeaderBytes:    32 << 10,
			TLSConfig:         server.tlsConfig,
		}
		server.sharing = shared
		go shared.Serve(tls.NewListener(listener, shared.TLSConfig))
	}
	if !args.Enabled && server.sharing != nil {
		shared := server.sharing
		server.sharing = nil
		server.pairs = map[string]*serverPair{}
		go func() {
			stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			shared.Shutdown(stop)
			shared.Close() // event and browser streams do not end on their own
		}()
	}
	server.broadcastLocked("sharing", map[string]bool{"enabled": server.sharing != nil})
	core.WriteJSON(w, 200, map[string]any{"enabled": server.sharing != nil, "port": server.sharingPort})
}

// answerPairing is POST /api/local/pairings/{id} {code, approve}: the phone's
// user typed the code the device shows (as in the server console), or
// refused it.
func (server *headlessServer) answerPairing(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Code    string
		Approve bool
	}
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, 400, "Invalid request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	id := r.PathValue("id")
	pair := server.pairs[id]
	if pair == nil || time.Now().After(pair.Expires) || pair.Tries >= 5 {
		core.Fail(w, 404, "This pairing has expired")
		return
	}
	if !args.Approve {
		delete(server.pairs, id)
		server.broadcastLocked("sharing", map[string]bool{"enabled": server.sharing != nil})
		core.WriteJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if subtle.ConstantTimeCompare([]byte(pair.Code), []byte(remoteapi.TokenHash(strings.TrimSpace(args.Code)))) != 1 {
		pair.Tries++
		core.Fail(w, 403, "This is not the code the device shows")
		return
	}
	pair.Approved = true
	server.broadcastLocked("sharing", map[string]bool{"enabled": server.sharing != nil})
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

// revokeDevice is POST /api/local/devices/{index}/revoke: the device's
// token stops working and its streams end.
func (server *headlessServer) revokeDevice(w http.ResponseWriter, r *http.Request) {
	index, err := strconv.Atoi(r.PathValue("index"))
	server.mu.Lock()
	defer server.mu.Unlock()
	if err != nil || index < 0 || index >= len(server.devices) {
		core.Fail(w, 404, "Unknown device")
		return
	}
	before := append([]remoteapi.PairedDevice{}, server.devices...)
	server.devices = append(server.devices[:index], server.devices[index+1:]...)
	if err := server.saveLocked(); err != nil {
		server.devices = before
		core.Fail(w, 500, err.Error())
		return
	}
	server.disconnectStreamsLocked() // the phone app reconnects at once
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}
