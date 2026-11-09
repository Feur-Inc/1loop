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
