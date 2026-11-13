// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client's paired servers: the page's localStorage, which only
// this origin (the server itself) can read.

import "syscall/js"

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
