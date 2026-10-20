package server

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"

	"1loop/internal/remoteapi"
)

// The phone creates this private token itself; it never goes through the
// network pairing flow or appears in process arguments or console output.
func (server *headlessServer) authorizeLocalClient(tokenPath, listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("Local phone mode requires a loopback listen address")
	}
	info, err := os.Stat(tokenPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("The local client token must be an owner-only regular file")
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(string(data))
	decoded, err := hex.DecodeString(token)
	if err != nil || len(decoded) < 24 {
		return fmt.Errorf("The local client token must contain at least 24 random bytes in hexadecimal")
	}
	server.local = true
	server.name = "This phone"
	server.localHash = remoteapi.TokenHash(token)
	// Devices paired while the phone was shared stay; the phone app's own
	// token, new at every start, was once kept among them.
	kept := server.devices[:0]
	for _, device := range server.devices {
		if device.Name != "Local phone" {
			kept = append(kept, device)
		}
	}
	server.devices = kept
	return nil
}
