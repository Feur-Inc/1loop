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

// Allows tells whether a token's hash is the phone app's or a paired
// device's (remoteapi.Engine).
func (server *headlessServer) Allows(hash string) bool {
	if hash == "" {
		return false
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	allowed := server.localHash != "" && subtle.ConstantTimeCompare([]byte(server.localHash), []byte(hash)) == 1
	for _, device := range server.devices {
		if subtle.ConstantTimeCompare([]byte(device.TokenHash), []byte(hash)) == 1 {
			allowed = true
		}
	}
	return allowed
}

// RequestPairing is POST /api/pair (remoteapi.Engine): the device gets
// the code to show, and the pairing waits for approval, unless it carries
// the setup link's secret.
func (server *headlessServer) RequestPairing(req *http.Request, args remoteapi.PairRequest) (map[string]any, error) {
	device := remoteapi.DeviceName(args.Device)
	if origin := foreignOrigin(req); origin != "" { // a page of another server (cors.go)
		device += " · " + origin
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	pair, err := server.pairing.Request(device, remoteapi.ClientAddress(req))
	if err != nil {
		return nil, err
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
	return map[string]any{"pairing": pair.ID, "code": pair.Code, "console": true, "phone": server.local, "approved": pair.Approved, "expiresIn": int(remoteapi.PairingTTL.Seconds()), "command": server.adminCommandFor(pair.ID, pair.Code)}, nil
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

// ConfirmPairing is POST /api/pair/confirm (remoteapi.Engine): an
// approved pairing's device gets its token.
func (server *headlessServer) ConfirmPairing(_ *http.Request, args remoteapi.PairConfirm) (map[string]any, error) {
	server.mu.Lock()
	defer server.mu.Unlock()
	pair, err := server.pairing.Confirm(args.Pairing, args.Code, args.Device)
	switch {
	case errors.Is(err, remoteapi.ErrPairingLocked):
		server.pairingLockedLocked()
		return nil, err
	case errors.Is(err, remoteapi.ErrTooManyTries):
		server.pairingChangedLocked()
		return nil, err
	case err != nil:
		return nil, err
	}
	token := core.Identifier()
	device := remoteapi.PairedDevice{Name: pair.Device, TokenHash: remoteapi.TokenHash(token), Added: time.Now()}
	server.devices = append(server.devices, device) // the same code cannot pair twice while the state is saved
	if err := server.saveOutsideLocked(); err != nil {
		server.devices = slices.DeleteFunc(server.devices, func(paired remoteapi.PairedDevice) bool { return paired.TokenHash == device.TokenHash })
		server.pairing.Restore(pair)
		return nil, remoteapi.StatusError{Status: 500, Err: errors.New("Unable to save paired device")}
	}
	server.setup = "" // a device is paired: no more setup link
	if server.local {
		server.broadcastLocked("sharing", map[string]bool{"enabled": server.sharing != nil})
	}
	return map[string]any{"token": token}, nil
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
	hash := remoteapi.RequestTokenHash(r)
	server.mu.Lock()
	defer server.mu.Unlock()
	devices := []map[string]any{}
	for _, device := range server.devices {
		current := subtle.ConstantTimeCompare([]byte(device.TokenHash), []byte(hash)) == 1
		devices = append(devices, map[string]any{"id": remoteapi.DeviceID(device), "name": device.Name, "added": device.Added, "current": current})
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
		return id != "" && remoteapi.DeviceID(device) == id
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
