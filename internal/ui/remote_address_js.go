// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client's paired server: the page's localStorage, which only
// this origin (the server itself) can read.

import (
	"encoding/json"
	"syscall/js"
)

const remoteAddressKey = "1loop.remote-server"

func loadServerAddress() serverAddress {
	var saved serverAddress
	if item := js.Global().Get("localStorage").Call("getItem", remoteAddressKey); item.Type() == js.TypeString {
		json.Unmarshal([]byte(item.String()), &saved)
	}
	return saved
}

func saveServerAddress(address serverAddress) error {
	data, _ := json.Marshal(address)
	js.Global().Get("localStorage").Call("setItem", remoteAddressKey, string(data))
	return nil
}

// The other servers the browser client linked (web_targets.go), and each
// server's projects as last listed, for a visit when it does not answer.
const (
	linkedServersKey = "1loop.linked-servers"
	webProjectsKey   = "1loop.server-projects"
)

func loadStored(key string, value any) {
	if item := js.Global().Get("localStorage").Call("getItem", key); item.Type() == js.TypeString {
		json.Unmarshal([]byte(item.String()), value)
	}
}

func store(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	js.Global().Get("localStorage").Call("setItem", key, string(data))
	return nil
}

func loadLinkedServers() []serverAddress {
	var linked []serverAddress
	loadStored(linkedServersKey, &linked)
	return linked
}

func saveLinkedServers(linked []serverAddress) error { return store(linkedServersKey, linked) }

func loadWebProjects() map[string][]string {
	projects := map[string][]string{}
	loadStored(webProjectsKey, &projects)
	return projects
}

func saveWebProjects(projects map[string][]string) { store(webProjectsKey, projects) }
