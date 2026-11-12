// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// The server last paired with, in the user's config folder (the browser
// client keeps it in the page's storage: remote_address_js.go).

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func remoteAddressPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "1loop", "remote-server.json")
}

func loadServerAddress() serverAddress {
	var saved serverAddress
	if data, err := os.ReadFile(remoteAddressPath()); err == nil {
		json.Unmarshal(data, &saved)
	}
	return saved
}

func saveServerAddress(address serverAddress) error {
	data, _ := json.Marshal(address)
	path := remoteAddressPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// The browser client's other servers and their projects are the page's
// (remote_address_js.go): the desktop has its one paired server.
func loadLinkedServers() []serverAddress      { return nil }
func saveLinkedServers([]serverAddress) error { return nil }
func loadWebProjects() map[string][]string    { return map[string][]string{} }
func saveWebProjects(map[string][]string)     {}
