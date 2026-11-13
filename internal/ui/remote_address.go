// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// The servers paired with, in the user's config folder (the browser
// client keeps them in the page's storage: remote_address_js.go).

import (
	"os"
	"path/filepath"
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
	path := remoteAddressPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, saved.encode(), 0600)
}

// forgetServerAddress unpairs a server; the others stay paired.
func forgetServerAddress(address string) error {
	saved := loadPairings()
	saved.remove(address)
	return os.WriteFile(remoteAddressPath(), saved.encode(), 0600)
}

// The browser client's servers' projects are kept in the page's storage
// (remote_address_js.go); the desktop keeps them in its settings.
func loadWebProjects() map[string][]string { return map[string][]string{} }
func saveWebProjects(map[string][]string)  {}
