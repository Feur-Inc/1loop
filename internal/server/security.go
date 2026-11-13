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
	"io"
	"math/big"
	"net/http"
	"os"
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
		fmt.Fprintf(os.Stderr, "\nPairing requested by %q. Approve it in 1loop on a paired device, or type: pair %s <code shown on that device>\n(or from another shell: %s)\n", args.Device, id, server.pairCommand(id, "<code>"))
		server.pairingChangedLocked()
	}
	core.WriteJSON(writer, 200, map[string]any{"pairing": id, "code": code, "console": true, "phone": server.local, "approved": pair.Approved, "expiresIn": 120, "command": server.pairCommand(id, code)})
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

// console reads the server's commands from its standard input.
func (server *headlessServer) console() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		server.command(strings.Fields(scanner.Text()), os.Stderr)
	}
}

// command runs one of the console's commands (pair, devices, revoke, ssh,
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
		pair := server.pairs[parts[1]]
		if pair == nil || time.Now().After(pair.Expires) || pair.Tries >= 5 {
			fmt.Fprintln(out, "Pairing expired or unknown")
			return false
		}
		hash := remoteapi.TokenHash(parts[2])
		if subtle.ConstantTimeCompare([]byte(pair.Code), []byte(hash)) != 1 {
			pair.Tries++
			fmt.Fprintln(out, "Incorrect code")
			return false
		}
		pair.Approved = true
		server.pairingChangedLocked()
		fmt.Fprintf(out, "Approved %q. Finish connecting on that device.\n", pair.Device)
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
		before := append([]remoteapi.PairedDevice{}, server.devices...)
		server.devices = append(server.devices[:index-1], server.devices[index:]...)
		if err := server.saveLocked(); err != nil {
			server.devices = before
			fmt.Fprintln(out, err)
			return false
		}
		server.disconnectStreamsLocked()
		fmt.Fprintln(out, "Device revoked")
		server.offerSetupLocked()
	case "ssh", "trust", "refuse":
		return server.machineConsole(parts, out)
	default:
		fmt.Fprintln(out, "Commands: pair <id> <code>, devices, revoke <number>, ssh, trust <number>, refuse <number>")
		return false
	}
	return true
}

func saveServerJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return core.WriteFileSafely(path, 0600, false, data)
}
