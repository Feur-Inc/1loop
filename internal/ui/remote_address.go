// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// The servers paired with, in the user's config folder (the browser
// client keeps them in the page's storage: remote_address_js.go).

import (
	"fmt"
	"os"
	"path/filepath"

	"1loop/internal/core"
)

func remoteAddressPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "1loop", "remote-server.json")
}

func loadPairings() pairings {
	data, _ := os.ReadFile(remoteAddressPath())
	return decodePairings(data)
}

// loadServerAddress is the server last paired.
func loadServerAddress() serverAddress { return loadPairings().serverAddress }

// loadServerAddresses are every server paired.
func loadServerAddresses() []serverAddress { return loadPairings().all() }

// saveServerAddress pairs a server: it becomes the last paired, and the
// others stay paired.
func saveServerAddress(address serverAddress) error {
	saved := loadPairings()
	saved.add(address)
	// Every paired server's token is in it: never half written (#523).
	return core.WriteFileSafely(remoteAddressPath(), 0600, false, saved.encode())
}

// saveLendBrowser turns lending this computer's browser to a server on or
// off.
func saveLendBrowser(address string, on bool) error {
	saved := loadPairings()
	if !saved.setLendBrowser(address, on) {
		return fmt.Errorf("This server is not paired")
	}
	return core.WriteFileSafely(remoteAddressPath(), 0600, false, saved.encode())
}

// forgetServerAddress unpairs a server; the others stay paired.
func forgetServerAddress(address string) error {
	saved := loadPairings()
	saved.remove(address)
	return core.WriteFileSafely(remoteAddressPath(), 0600, false, saved.encode())
}

// The browser client's servers' projects are kept in the page's storage
// (remote_address_js.go); the desktop keeps them in its settings.
func loadWebProjects() map[string][]string { return map[string][]string{} }
func saveWebProjects(map[string][]string)  {}

// pairOriginAgain is the browser client's (web_js.go): the desktop has no
// page's own server.
func (ui *desktopUI) pairOriginAgain() {}
