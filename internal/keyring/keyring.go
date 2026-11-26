// SPDX-License-Identifier: MPL-2.0

// Package keyring keeps secrets in the system's secret store: the Secret
// Service (GNOME Keyring, KWallet) over D-Bus on Linux, the Credential
// Manager on Windows, the Keychain on macOS (#510). It needs no cgo and no
// dependency beyond the D-Bus one 1loop already has.
package keyring

import "errors"

// Service names 1loop's entries in the store.
const Service = "1loop"

var (
	// ErrNotFound: the store has no secret under that name.
	ErrNotFound = errors.New("keyring: secret not found")
	// ErrUnavailable: there is no store here (no session bus, no Secret
	// Service, an unsupported system).
	ErrUnavailable = errors.New("keyring: no secret store")
)

// Store keeps one secret per account name. Its calls may block (a locked
// keyring asks for its password): they are made off the interface thread.
type Store interface {
	Get(account string) (string, error)
	Set(account, secret string) error
	Delete(account string) error // no error when there was none
}

// System is this system's store, nil where there is none.
func System() Store { return system() }
