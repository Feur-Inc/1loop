// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

// The vault: sign-ins, keys and the passwords of the accounts, encrypted
// (AES-256-GCM) with a key kept apart, where the system keeps secrets
// when it does (key_windows.go, key_other.go). Loopy's model never sees a
// value: 1loop fills it in the page itself.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"1loop/internal/loopy"
)

type secretEntry struct {
	loopy.VaultEntry
	Values map[string]string `json:"values"`
}

type vault struct {
	mu      sync.Mutex
	path    string
	seal    cipher.AEAD
	entries []secretEntry
}

// openVault reads the vault kept in dir. One whose key is gone is refused
// rather than replaced.
func openVault(dir string) (*vault, error) {
	v := &vault{path: filepath.Join(dir, "vault")}
	data, err := os.ReadFile(v.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key, err := vaultKey(dir, len(data) == 0)
	if err != nil {
		return nil, fmt.Errorf("Loopy's vault cannot be opened: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if v.seal, err = cipher.NewGCM(block); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return v, nil
	}
	size := v.seal.NonceSize()
	if len(data) < size {
		return nil, errors.New("Loopy's vault is damaged")
	}
	clear, err := v.seal.Open(nil, data[:size], data[size:], []byte("1loop loopy vault 1"))
	if err != nil {
		return nil, errors.New("Loopy's vault cannot be opened with this key")
	}
	return v, json.Unmarshal(clear, &v.entries)
}

func (v *vault) save() error {
	clear, err := json.Marshal(v.entries)
	if err != nil {
		return err
	}
	nonce := make([]byte, v.seal.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if err := os.WriteFile(v.path+".tmp", v.seal.Seal(nonce, nonce, clear, []byte("1loop loopy vault 1")), 0600); err != nil {
		return err
	}
	return os.Rename(v.path+".tmp", v.path)
}

// put keeps an entry; one with the same id is replaced.
func (v *vault) put(entry loopy.VaultEntry, values map[string]string) (loopy.VaultEntry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if entry.ID == "" {
		entry.ID = loopy.NewID()
	}
	entry.Fields = nil
	for name := range values {
		entry.Fields = append(entry.Fields, name)
	}
	sort.Strings(entry.Fields)
	kept := []secretEntry{}
	for _, e := range v.entries {
		if e.ID != entry.ID {
			kept = append(kept, e)
		}
	}
	old := v.entries
	v.entries = append(kept, secretEntry{entry, values})
	if err := v.save(); err != nil {
		v.entries = old
		return entry, err
	}
	return entry, nil
}

func (v *vault) remove(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	kept := []secretEntry{}
	for _, e := range v.entries {
		if e.ID != id {
			kept = append(kept, e)
		}
	}
	v.entries = kept
	return v.save()
}

// list is the entries without their values; hidden ones (the accounts'
// passwords) are left out.
func (v *vault) list() []loopy.VaultEntry {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []loopy.VaultEntry
	for _, e := range v.entries {
		if e.Kind != "account" {
			out = append(out, e.VaultEntry)
		}
	}
	return out
}

func (v *vault) get(id string) (secretEntry, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, e := range v.entries {
		if e.ID == id {
			return e, true
		}
	}
	return secretEntry{}, false
}

// forSite are the sign-ins a page at host may get.
func (v *vault) forSite(host string) []secretEntry {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []secretEntry
	for _, e := range v.entries {
		if e.Kind == "signin" && sameSite(host, e.Site) {
			out = append(out, e)
		}
	}
	return out
}

// secrets are the values that must never be read or sent: not the user
// names and addresses.
func (v *vault) secrets() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []string
	for _, e := range v.entries {
		for name, value := range e.Values {
			if e.Kind != "contact" && name != "user" && value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}
