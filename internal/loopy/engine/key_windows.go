// SPDX-License-Identifier: MPL-2.0

package engine

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The vault's key is kept encrypted by Windows for this user (DPAPI).

func blob(data []byte) windows.DataBlob {
	if len(data) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

func taken(out windows.DataBlob) []byte {
	data := append([]byte(nil), unsafe.Slice(out.Data, out.Size)...)
	windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return data
}

func protectKey(key []byte) ([]byte, error) {
	in, out := blob(key), windows.DataBlob{}
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return taken(out), nil
}

func unprotectKey(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("no key")
	}
	in, out := blob(data), windows.DataBlob{}
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return taken(out), nil
}
