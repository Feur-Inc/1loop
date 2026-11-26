// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// vaultKey is the vault's key; a new one is made only when fresh.
func vaultKey(dir string, fresh bool) ([]byte, error) {
	path := filepath.Join(dir, "vault.key")
	data, err := os.ReadFile(path)
	if err == nil {
		key, err := unprotectKey(data)
		if err == nil && len(key) != 32 {
			err = errors.New("its key is damaged")
		}
		return key, err
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !fresh {
		return nil, errors.New("its key is gone")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	kept, err := protectKey(key)
	if err != nil {
		return nil, err
	}
	return key, os.WriteFile(path, kept, 0600)
}
