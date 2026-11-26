// SPDX-License-Identifier: MPL-2.0

package server

// The headless server's security: its TLS certificate, device pairing
// (the first device from a one-time setup link, the next ones approved from
// a paired device or the console), and the tokens paired devices carry.

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

func (server *headlessServer) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" || token == req.Header.Get("Authorization") {
			core.Fail(writer, 401, "Pair this device with the server first")
			return
		}
		hash := remoteapi.TokenHash(token)
		server.mu.Lock()
		allowed := server.localHash != "" && subtle.ConstantTimeCompare([]byte(server.localHash), []byte(hash)) == 1
		for _, device := range server.devices {
			if subtle.ConstantTimeCompare([]byte(device.TokenHash), []byte(hash)) == 1 {
				allowed = true
			}
		}
		server.mu.Unlock()
		if !allowed {
			core.Fail(writer, 401, "This device is not paired with the server")
			return
		}
		writer.Header().Set("Cache-Control", "no-store")
		next(writer, req)
	}
}

func (server *headlessServer) pair(writer http.ResponseWriter, req *http.Request) {
	var args struct{ Device, Setup string }
	if core.ReadJSON(req, &args) != nil {
		core.Fail(writer, 400, "Provide a device name")
		return
	}
	device := remoteapi.DeviceName(args.Device)
	if origin := foreignOrigin(req); origin != "" { // a page of another server (cors.go)
		device += " · " + origin
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	pair, err := server.pairing.Request(device, remoteapi.ClientAddress(req))
	if err != nil {
		core.Fail(writer, 429, err.Error())
		return
	}
	if server.setup != "" && len(server.devices) == 0 && subtle.ConstantTimeCompare([]byte(server.setup), []byte(args.Setup)) == 1 {
		// The setup link: approved at once, and used up.
		server.pairing.Approve(pair.ID, pair.Code)
		pair.Approved, server.setup = true, ""
		fmt.Fprintf(os.Stderr, "\n%q paired with the setup link.\n", pair.Device)
	} else {
		fmt.Fprintf(os.Stderr, "\nPairing requested by %q. Approve it in 1loop on a paired device, or type: pair %s <code shown on that device>\n", pair.Device, pair.ID)
		if server.adminReady.Load() { // "1loop-server pair" reaches this server only through its admin socket
			fmt.Fprintf(os.Stderr, "(or from another shell: %s)\n", server.pairCommand(pair.ID, "<code>"))
		}
		server.pairingChangedLocked()
	}
	core.WriteJSON(writer, 200, map[string]any{"pairing": pair.ID, "code": pair.Code, "console": true, "phone": server.local, "approved": pair.Approved, "expiresIn": int(remoteapi.PairingTTL.Seconds()), "command": server.adminCommandFor(pair.ID, pair.Code)})
}

// pairingChangedLocked tells the clients that can approve pairings: the
// phone's app in local mode, the paired devices otherwise.
func (server *headlessServer) pairingChangedLocked() {
	if server.local {
		server.broadcastLocked("sharing", map[string]bool{"enabled": server.sharing != nil})
	} else {
		server.broadcastLocked("pairing", map[string]any{"pending": server.pendingPairsLocked()})
	}
}

// pendingPairsLocked are the pairings waiting for approval.
func (server *headlessServer) pendingPairsLocked() []map[string]any {
	pending := []map[string]any{}
	for _, pair := range server.pairing.Waiting() {
		pending = append(pending, map[string]any{"id": pair.ID, "device": pair.Device, "expiresAt": pair.Expires.UnixMilli()})
	}
	return pending
}

// pairings is GET /api/pairings: the pairings a paired device can approve.
func (server *headlessServer) pairings(w http.ResponseWriter, r *http.Request) {
	server.mu.Lock()
	defer server.mu.Unlock()
	core.WriteJSON(w, 200, map[string]any{"pending": server.pendingPairsLocked()})
}

// offerSetupLocked prints a one-time setup link while no device is paired:
// opening it in a browser pairs that browser without the console. The
// secret is in the URL's fragment, which browsers never send in a request.
func (server *headlessServer) offerSetupLocked() {
	if server.local || server.webDir == "" || len(server.devices) > 0 {
		return
	}
	server.setup = core.Identifier()
	fmt.Fprint(os.Stderr, setupMessage(server.setupPort, server.setup, server.proxied))
}

// setupMessage is the setup link's announcement: behind a reverse proxy,
// the link is the proxy's address, without the server's own port.
func setupMessage(port int, secret string, proxied bool) string {
	if proxied {
		return fmt.Sprintf("\nNo device is paired yet. To pair the first one, open this link in a browser,\nwith your reverse proxy's address in place of <proxy> (it works once):\n\n  https://<proxy>/app/#setup=%s\n\nWithout the proxy: https://<server>:%d/app/#setup=%s\n\n", secret, port, secret)
	}
	return fmt.Sprintf("\nNo device is paired yet. To pair the first one, open this link in a browser,\nwith this server's address in place of <server> (it works once):\n\n  https://<server>:%d/app/#setup=%s\n\n", port, secret)
}

func (server *headlessServer) confirmPair(writer http.ResponseWriter, req *http.Request) {
	var args struct{ Pairing, Code, Device string }
	if core.ReadJSON(req, &args) != nil {
		core.Fail(writer, 400, "Invalid pairing request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	pair, err := server.pairing.Confirm(args.Pairing, args.Code, args.Device)
	switch {
	case errors.Is(err, remoteapi.ErrPairingNotApproved):
		core.Fail(writer, 409, err.Error())
		return
	case errors.Is(err, remoteapi.ErrPairingLocked):
		server.pairingLockedLocked()
		core.Fail(writer, 403, err.Error())
		return
	case err != nil:
		if errors.Is(err, remoteapi.ErrTooManyTries) {
			server.pairingChangedLocked()
		}
		core.Fail(writer, 403, err.Error())
		return
	}
	token := core.Identifier()
	device := remoteapi.PairedDevice{Name: pair.Device, TokenHash: remoteapi.TokenHash(token), Added: time.Now()}
	server.devices = append(server.devices, device) // the same code cannot pair twice while the state is saved
	if err := server.saveOutsideLocked(); err != nil {
		server.devices = slices.DeleteFunc(server.devices, func(paired remoteapi.PairedDevice) bool { return paired.TokenHash == device.TokenHash })
		server.pairing.Restore(pair)
		core.Fail(writer, 500, "Unable to save paired device")
		return
	}
	server.setup = "" // a device is paired: no more setup link
	if server.local {
		server.broadcastLocked("sharing", map[string]bool{"enabled": server.sharing != nil})
	}
	core.WriteJSON(writer, 200, map[string]string{"token": token})
}

// pairingLockedLocked tells the console that too many wrong codes stopped
// pairing, and how to resume it.
func (server *headlessServer) pairingLockedLocked() {
	if !server.local {
		fmt.Fprintln(os.Stderr, "\nToo many wrong pairing codes: pairing is paused. Type unlock here (or run 1loop-server unlock) to resume it.")
	}
	server.pairingChangedLocked()
}

// console reads the server's commands from its standard input.
func (server *headlessServer) console() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		server.command(strings.Fields(scanner.Text()), os.Stderr)
	}
}

// command runs one of the console's commands (pair, unlock, devices, revoke, ssh,
// trust, refuse), typed in the console or sent to the admin socket by
// "1loop-server pair …" (admin.go), and tells whether it succeeded.
func (server *headlessServer) command(parts []string, out io.Writer) bool {
	if len(parts) == 0 {
		return true
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	switch parts[0] {
	case "pair":
		if len(parts) != 3 {
			fmt.Fprintln(out, "Usage: pair <pairing ID> <six-digit code>")
			return false
		}
		pair, _ := server.pairing.Get(parts[1])
		switch err := server.pairing.Approve(parts[1], parts[2]); {
		case errors.Is(err, remoteapi.ErrPairingExpired):
			fmt.Fprintln(out, "Pairing expired or unknown")
			return false
		case err != nil:
			fmt.Fprintln(out, "Incorrect code")
			server.pairingChangedLocked()
			return false
		}
		server.pairingChangedLocked()
		fmt.Fprintf(out, "Approved %q. Finish connecting on that device.\n", pair.Device)
	case "unlock":
		server.pairing.Unlock()
		fmt.Fprintln(out, "Pairing resumed")
	case "devices":
		if len(server.devices) == 0 {
			fmt.Fprintln(out, "No paired devices")
		}
		for index, device := range server.devices {
			fmt.Fprintf(out, "%d  %s  %s\n", index+1, device.Name, device.Added.Format(time.RFC3339))
		}
	case "revoke":
		if len(parts) != 2 {
			fmt.Fprintln(out, "Usage: revoke <device number from devices>")
			return false
		}
		var index int
		_, err := fmt.Sscan(parts[1], &index)
		if err != nil || index < 1 || index > len(server.devices) {
			fmt.Fprintln(out, "Unknown device")
			return false
		}
		if err := server.removeDeviceLocked(index - 1); err != nil {
			fmt.Fprintln(out, err)
			return false
		}
		fmt.Fprintln(out, "Device revoked")
	case "ssh", "trust", "refuse":
		return server.machineConsole(parts, out)
	default:
		fmt.Fprintln(out, "Commands: pair <id> <code>, unlock, devices, revoke <number>, ssh, trust <number>, refuse <number>")
		return false
	}
	return true
}

// deviceID names a paired device in the API: the start of its token's
// hash, which tells nothing of the token and stays the same across restarts.
func deviceID(device remoteapi.PairedDevice) string {
	if len(device.TokenHash) < 16 {
		return device.TokenHash
	}
	return device.TokenHash[:16]
}

// requestHash is the hash of the token a request carries, "" without one.
func requestHash(req *http.Request) string {
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if token == "" || token == req.Header.Get("Authorization") {
		return ""
	}
	return remoteapi.TokenHash(token)
}

// removeDeviceLocked unpairs the device at index: its token stops working
// on its next request, and the open streams end so that no device keeps one
// it no longer may (the others reconnect at once). With no device left, a
// new setup link is offered.
func (server *headlessServer) removeDeviceLocked(index int) error {
	if index < 0 || index >= len(server.devices) {
		return fmt.Errorf("Unknown device")
	}
	removed := server.devices[index]
	server.devices = slices.Delete(slices.Clone(server.devices), index, index+1)
	server.disconnectStreamsLocked() // before the save: its streams end now
	if err := server.saveOutsideLocked(); err != nil {
		if !slices.ContainsFunc(server.devices, func(paired remoteapi.PairedDevice) bool { return paired.TokenHash == removed.TokenHash }) {
			server.devices = append(server.devices, removed)
		}
		return err
	}
	if !server.local {
		fmt.Fprintf(os.Stderr, "\n%q is no longer paired.\n", removed.Name)
	}
	server.offerSetupLocked()
	return nil
}

// pairedDevices is GET /api/devices: the devices paired with the server,
// the one asking marked current (#428).
func (server *headlessServer) pairedDevices(w http.ResponseWriter, r *http.Request) {
	hash := requestHash(r)
	server.mu.Lock()
	defer server.mu.Unlock()
	devices := []map[string]any{}
	for _, device := range server.devices {
		current := subtle.ConstantTimeCompare([]byte(device.TokenHash), []byte(hash)) == 1
		devices = append(devices, map[string]any{"id": deviceID(device), "name": device.Name, "added": device.Added, "current": current})
	}
	core.WriteJSON(w, 200, map[string]any{"devices": devices})
}

// revokePairedDevice is POST /api/devices/{id}/revoke: a paired device
// unpairs another one, or itself when it forgets the server (#428).
func (server *headlessServer) revokePairedDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server.mu.Lock()
	defer server.mu.Unlock()
	index := slices.IndexFunc(server.devices, func(device remoteapi.PairedDevice) bool {
		return id != "" && deviceID(device) == id
	})
	if index < 0 {
		core.Fail(w, 404, "This device is not paired")
		return
	}
	if err := server.removeDeviceLocked(index); err != nil {
		core.Fail(w, 500, "Unable to unpair the device: "+err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"ok": true})
}

func saveServerJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return core.WriteFileSafely(path, 0600, false, data)
}

// Pending pairings: a few per client address, and a bound on them all.
const (
	pairingsPerAddress = 2
	maxPendingPairings = 32
)
