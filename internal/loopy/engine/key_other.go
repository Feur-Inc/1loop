// SPDX-License-Identifier: MPL-2.0

//go:build !js && !windows

package engine

// The vault's key is kept in a file of the user's alone, as 1loop
// keeps its connections' keys: a server and the phone have no keyring to
// unlock without someone there. On Android the file is in the app's own
// storage, encrypted with the phone.
func protectKey(key []byte) ([]byte, error)   { return key, nil }
func unprotectKey(key []byte) ([]byte, error) { return key, nil }
