// SPDX-License-Identifier: MPL-2.0

package keyring

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// The login Keychain, through /usr/bin/security: generic passwords of
// service "1loop", one per account.
type keychain struct{}

func system() Store { return keychain{} }

const security = "/usr/bin/security"

// itemNotFound is security's exit status for a missing item.
const itemNotFound = 44

func (keychain) Get(account string) (string, error) {
	out, err := exec.Command(security, "find-generic-password", "-s", Service, "-a", account, "-w").Output()
	if err != nil {
		return "", keychainError(err)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

func (keychain) Set(account, key string) error {
	// Through security's own prompt (-i) rather than its arguments, which
	// every process could read; -X takes the secret in hexadecimal.
	command := exec.Command(security, "-i")
	command.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %q -a %q -X %s\n", Service, account, hex.EncodeToString([]byte(key))))
	if out, err := command.CombinedOutput(); err != nil || strings.Contains(string(out), "error") {
		if err == nil {
			err = errors.New(strings.TrimSpace(string(out)))
		}
		return keychainError(err)
	}
	return nil
}

func (keychain) Delete(account string) error {
	err := exec.Command(security, "delete-generic-password", "-s", Service, "-a", account).Run()
	if err = keychainError(err); errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func keychainError(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == itemNotFound {
		return ErrNotFound
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}
