// SPDX-License-Identifier: MPL-2.0

package ui

// Every server this window paired with (#383). The stored record keeps the
// server last paired at its top, as it always had it, so an older 1loop
// still reads it as its one server; the list beside it holds each one
// paired, in the order they were.

import (
	"encoding/json"
	"slices"
)

type pairings struct {
	serverAddress                 // the server last paired
	Servers       []serverAddress `json:"servers,omitempty"` // every server paired, the last one among them
}

func decodePairings(data []byte) pairings {
	var saved pairings
	json.Unmarshal(data, &saved)
	return saved
}

// all are the servers paired, an older record's one server included: those
// with an address and a token.
func (saved pairings) all() []serverAddress {
	servers := saved.Servers
	if len(servers) == 0 && saved.URL != "" { // written before #383
		servers = []serverAddress{saved.serverAddress}
	}
	var paired []serverAddress
	for _, server := range servers {
		if server.URL != "" && server.Token != "" {
			paired = append(paired, server)
		}
	}
	return paired
}

// add makes a server the last paired, in its place in the list when it
// was already paired, else after the others.
func (saved *pairings) add(address serverAddress) {
	if len(saved.Servers) == 0 && saved.URL != "" && saved.URL != address.URL {
		saved.Servers = []serverAddress{saved.serverAddress} // written before #383: its server stays paired
	}
	saved.serverAddress = address
	if i := slices.IndexFunc(saved.Servers, func(server serverAddress) bool { return server.URL == address.URL }); i >= 0 {
		saved.Servers[i] = address
		return
	}
	saved.Servers = append(saved.Servers, address)
}

func (saved pairings) encode() []byte {
	data, _ := json.Marshal(saved)
	return data
}

// pairedServer is the paired server at an address.
func pairedServer(address string) (serverAddress, bool) {
	for _, server := range loadServerAddresses() {
		if server.URL == address {
			return server, true
		}
	}
	return serverAddress{}, false
}
