// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client's paired servers: the page's localStorage, which only
// this origin (the server itself) can read.

import (
	"encoding/json"
	"syscall/js"
)

const remoteAddressKey = "1loop.remote-server"

func loadPairings() pairings {
	var data []byte
	if item := js.Global().Get("localStorage").Call("getItem", remoteAddressKey); item.Type() == js.TypeString {
		data = []byte(item.String())
	}
	return decodePairings(data)
}

func loadServerAddress() serverAddress { return loadPairings().serverAddress }

func loadServerAddresses() []serverAddress { return loadPairings().all() }

func saveServerAddress(address serverAddress) error {
	saved := loadPairings()
	saved.add(address)
	js.Global().Get("localStorage").Call("setItem", remoteAddressKey, string(saved.encode()))
	return nil
}

func forgetServerAddress(address string) error {
	saved := loadPairings()
	saved.remove(address)
	js.Global().Get("localStorage").Call("setItem", remoteAddressKey, string(saved.encode()))
	return nil
}

// Each of the browser client's servers' projects as last listed, for a
// visit when it does not answer. The servers themselves are the pairings
// above: the page's own and those it linked (web_targets.go).
const webProjectsKey = "1loop.server-projects"

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

func loadWebProjects() map[string][]string {
	projects := map[string][]string{}
	loadStored(webProjectsKey, &projects)
	return projects
}

func saveWebProjects(projects map[string][]string) { store(webProjectsKey, projects) }
