// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
)

// RequestTokenHash is the hash of the bearer token a request carries, ""
// without one.
func RequestTokenHash(req *http.Request) string {
	header := req.Header.Get("Authorization")
	token := strings.TrimPrefix(header, "Bearer ")
	if token == "" || token == header {
		return ""
	}
	return TokenHash(token)
}

// Tokens are the hashes of the paired devices' tokens. Every request is
// checked against them under their own small lock, so a phone's request
// never waits for the window or the engine to be free (#518).
type Tokens struct {
	mu     sync.RWMutex
	hashes []string
}

// Set replaces the tokens with the devices'.
func (tokens *Tokens) Set(devices []PairedDevice) {
	hashes := make([]string, 0, len(devices))
	for _, device := range devices {
		hashes = append(hashes, device.TokenHash)
	}
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	tokens.hashes = hashes
}

// Allows tells whether hash is a paired device's, comparing every one in
// constant time.
func (tokens *Tokens) Allows(hash string) bool {
	if hash == "" {
		return false
	}
	tokens.mu.RLock()
	defer tokens.mu.RUnlock()
	allowed := false
	for _, known := range tokens.hashes {
		if subtle.ConstantTimeCompare([]byte(known), []byte(hash)) == 1 {
			allowed = true
		}
	}
	return allowed
}

// DeviceID names a paired device in the API (GET /api/devices): the start
// of its token's hash, which tells nothing of the token and stays the same
// across restarts.
func DeviceID(device PairedDevice) string {
	if len(device.TokenHash) < 16 {
		return device.TokenHash
	}
	return device.TokenHash[:16]
}
