package server

// The headless server's security: its TLS certificate, device pairing
// approved from the console, and the tokens paired devices carry.

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
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
		allowed := false
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
	var args struct{ Device string }
	if core.ReadJSON(req, &args) != nil || strings.TrimSpace(args.Device) == "" || len(args.Device) > 120 {
		core.Fail(writer, 400, "Provide a device name")
		return
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
	server.pairs[id] = &serverPair{Device: args.Device, Code: remoteapi.TokenHash(code), Expires: time.Now().Add(2 * time.Minute)}
	fmt.Fprintf(os.Stderr, "\nPairing requested by %q. Type: pair %s <code shown on that device>\n", args.Device, id)
	core.WriteJSON(writer, 200, map[string]any{"pairing": id, "code": code, "console": true, "expiresIn": 120})
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
		core.Fail(writer, 409, "Waiting for approval in the server console")
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
			before := append([]remoteapi.PairedDevice{}, server.devices...)
			server.devices = append(server.devices[:index-1], server.devices[index:]...)
			if err := server.saveLocked(); err != nil {
				server.devices = before
				fmt.Fprintln(os.Stderr, err)
			} else {
				server.disconnectStreamsLocked()
				fmt.Fprintln(os.Stderr, "Device revoked")
			}
		default:
			fmt.Fprintln(os.Stderr, "Commands: pair <id> <code>, devices, revoke <number>")
		}
		server.mu.Unlock()
	}
}

func saveServerJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".server-state-*")
	if err != nil {
		return err
	}
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
