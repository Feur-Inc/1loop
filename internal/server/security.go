// SPDX-License-Identifier: MPL-2.0

package server

// The headless server's security: its TLS certificate, device pairing
// (the first device from a one-time setup link, the next ones approved from
// a paired device or the console), and the tokens paired devices carry.

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

type serverPair struct {
	Device, Code string
	Expires      time.Time
	Approved     bool
	Tries        int
}

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
	if core.ReadJSON(req, &args) != nil || strings.TrimSpace(args.Device) == "" || len(args.Device) > 120 {
		core.Fail(writer, 400, "Provide a device name")
		return
	}
	if origin := foreignOrigin(req); origin != "" { // a page of another server (cors.go)
		args.Device += " · " + origin
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	for id, pair := range server.pairs {
		if time.Now().After(pair.Expires) {
			delete(server.pairs, id)
		}
	}
	if len(server.pairs) >= 8 {
		core.Fail(writer, 429, "Too many pending pairings; try again later")
		return
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		core.Fail(writer, 500, "Unable to create pairing")
		return
	}
	id := core.Identifier()[:12]
	code := fmt.Sprintf("%06d", number)
	pair := &serverPair{Device: args.Device, Code: remoteapi.TokenHash(code), Expires: time.Now().Add(2 * time.Minute)}
	server.pairs[id] = pair
	if server.setup != "" && len(server.devices) == 0 && subtle.ConstantTimeCompare([]byte(server.setup), []byte(args.Setup)) == 1 {
		// The setup link: approved at once, and used up.
		pair.Approved, server.setup = true, ""
		fmt.Fprintf(os.Stderr, "\n%q paired with the setup link.\n", args.Device)
	} else {
		fmt.Fprintf(os.Stderr, "\nPairing requested by %q. Approve it in 1loop on a paired device, or type: pair %s <code shown on that device>\n", args.Device, id)
		server.pairingChangedLocked()
	}
	core.WriteJSON(writer, 200, map[string]any{"pairing": id, "code": code, "console": true, "phone": server.local, "approved": pair.Approved, "expiresIn": 120})
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
	for id, pair := range server.pairs {
		if time.Now().Before(pair.Expires) && !pair.Approved && pair.Tries < 5 {
			pending = append(pending, map[string]any{"id": id, "device": pair.Device, "expiresAt": pair.Expires.UnixMilli()})
		}
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
	fmt.Fprintf(os.Stderr, "\nNo device is paired yet. To pair the first one, open this link in a browser,\nwith this server's address in place of <server> (it works once):\n\n  https://<server>:%d/app/#setup=%s\n\n", server.setupPort, server.setup)
}

func (server *headlessServer) confirmPair(writer http.ResponseWriter, req *http.Request) {
	var args struct{ Pairing, Code string }
	if core.ReadJSON(req, &args) != nil {
		core.Fail(writer, 400, "Invalid pairing request")
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	pair := server.pairs[args.Pairing]
	if pair == nil || time.Now().After(pair.Expires) || pair.Tries >= 5 {
		core.Fail(writer, 403, "Pairing expired; start again")
		return
	}
	hash := remoteapi.TokenHash(args.Code)
	if subtle.ConstantTimeCompare([]byte(pair.Code), []byte(hash)) != 1 {
		pair.Tries++
		core.Fail(writer, 403, "Incorrect pairing code")
		return
	}
	if !pair.Approved {
		core.Fail(writer, 409, "Waiting for approval on a paired device or in the server console")
		return
	}
	token := core.Identifier()
	device := remoteapi.PairedDevice{Name: pair.Device, TokenHash: remoteapi.TokenHash(token), Added: time.Now()}
	server.devices = append(server.devices, device)
	if err := server.saveLocked(); err != nil {
		server.devices = server.devices[:len(server.devices)-1]
		core.Fail(writer, 500, "Unable to save paired device")
		return
	}
	delete(server.pairs, args.Pairing)
	server.setup = "" // a device is paired: no more setup link
	if server.local {
		server.broadcastLocked("sharing", map[string]bool{"enabled": server.sharing != nil})
	}
	core.WriteJSON(writer, 200, map[string]string{"token": token})
}

func (server *headlessServer) console() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 0 {
			continue
		}
		server.mu.Lock()
		switch parts[0] {
		case "pair":
			if len(parts) != 3 {
				fmt.Fprintln(os.Stderr, "Usage: pair <pairing ID> <six-digit code>")
				break
			}
			pair := server.pairs[parts[1]]
			if pair == nil || time.Now().After(pair.Expires) || pair.Tries >= 5 {
				fmt.Fprintln(os.Stderr, "Pairing expired or unknown")
				break
			}
			hash := remoteapi.TokenHash(parts[2])
			if subtle.ConstantTimeCompare([]byte(pair.Code), []byte(hash)) != 1 {
				pair.Tries++
				fmt.Fprintln(os.Stderr, "Incorrect code")
				break
			}
			pair.Approved = true
			server.pairingChangedLocked()
			fmt.Fprintf(os.Stderr, "Approved %q. Finish connecting on that device.\n", pair.Device)
		case "devices":
			for index, device := range server.devices {
				fmt.Fprintf(os.Stderr, "%d  %s  %s\n", index+1, device.Name, device.Added.Format(time.RFC3339))
			}
		case "revoke":
			if len(parts) != 2 {
				fmt.Fprintln(os.Stderr, "Usage: revoke <device number from devices>")
				break
			}
			var index int
			_, err := fmt.Sscan(parts[1], &index)
			if err != nil || index < 1 || index > len(server.devices) {
				fmt.Fprintln(os.Stderr, "Unknown device")
				break
			}
			if err := server.removeDeviceLocked(index - 1); err != nil {
				fmt.Fprintln(os.Stderr, err)
			} else {
				fmt.Fprintln(os.Stderr, "Device revoked")
			}
		case "ssh", "trust", "refuse":
			server.machineConsole(parts)
		default:
			fmt.Fprintln(os.Stderr, "Commands: pair <id> <code>, devices, revoke <number>, ssh, trust <number>, refuse <number>")
		}
		server.mu.Unlock()
	}
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
	before := append([]remoteapi.PairedDevice{}, server.devices...)
	server.devices = append(server.devices[:index], server.devices[index+1:]...)
	if err := server.saveLocked(); err != nil {
		server.devices = before
		return err
	}
	server.disconnectStreamsLocked()
	if !server.local {
		fmt.Fprintf(os.Stderr, "\n%q is no longer paired.\n", before[index].Name)
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
