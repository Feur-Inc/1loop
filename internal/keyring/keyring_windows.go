// SPDX-License-Identifier: MPL-2.0

package keyring

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Credential Manager, through advapi32's Cred* calls: generic
// credentials named "1loop:<account>", for this user on this machine.
var (
	advapi32   = windows.NewLazySystemDLL("advapi32.dll")
	credWrite  = advapi32.NewProc("CredWriteW")
	credRead   = advapi32.NewProc("CredReadW")
	credDelete = advapi32.NewProc("CredDeleteW")
	credFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

// credential is CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type credentialManager struct{}

func system() Store { return credentialManager{} }

func target(account string) (*uint16, error) {
	return windows.UTF16PtrFromString(Service + ":" + account)
}

func (credentialManager) Get(account string) (string, error) {
	name, err := target(account)
	if err != nil {
		return "", err
	}
	var found *credential
	if ok, _, err := credRead.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&found))); ok == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return "", ErrNotFound
		}
		return "", err
	}
	defer credFree.Call(uintptr(unsafe.Pointer(found)))
	return string(unsafe.Slice(found.CredentialBlob, found.CredentialBlobSize)), nil
}

func (credentialManager) Set(account, key string) error {
	name, err := target(account)
	if err != nil {
		return err
	}
	user, _ := windows.UTF16PtrFromString(account)
	blob := []byte(key)
	entry := credential{Type: credTypeGeneric, TargetName: name, UserName: user, Persist: credPersistLocalMachine, CredentialBlobSize: uint32(len(blob))}
	if len(blob) > 0 {
		entry.CredentialBlob = &blob[0]
	}
	if ok, _, err := credWrite.Call(uintptr(unsafe.Pointer(&entry)), 0); ok == 0 {
		return err
	}
	return nil
}

func (credentialManager) Delete(account string) error {
	name, err := target(account)
	if err != nil {
		return err
	}
	if ok, _, err := credDelete.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0); ok == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return err
	}
	return nil
}
