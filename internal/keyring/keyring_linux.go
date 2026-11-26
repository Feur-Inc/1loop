// SPDX-License-Identifier: MPL-2.0

//go:build linux && !android

package keyring

import (
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// The Secret Service API (freedesktop.org), which GNOME Keyring and
// KWallet (5.97 and later) provide.
const (
	secretsName    = "org.freedesktop.secrets"
	secretsPath    = dbus.ObjectPath("/org/freedesktop/secrets")
	serviceIface   = "org.freedesktop.Secret.Service"
	itemIface      = "org.freedesktop.Secret.Item"
	promptIface    = "org.freedesktop.Secret.Prompt"
	defaultAlias   = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
	loginPath      = dbus.ObjectPath("/org/freedesktop/secrets/collection/login")
	noPrompt       = dbus.ObjectPath("/")
	promptDeadline = 2 * time.Minute // the unlock dialog, left open
)

type secretService struct{}

func system() Store { return secretService{} }

// secret is the Secret Service's Secret struct, (oayays).
type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// session is one exchange with the service, on a bus connection of its own:
// the shared one hands every signal to every listener, which the tray does
// not stand (ui/notify_linux.go).
type session struct {
	conn *dbus.Conn
	path dbus.ObjectPath
}

func open() (*session, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var output dbus.Variant
	var path dbus.ObjectPath
	// "plain": the secret travels unencrypted on the user's own session bus,
	// as with libsecret's default.
	if err = conn.Object(secretsName, secretsPath).Call(serviceIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&output, &path); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return &session{conn: conn, path: path}, nil
}

func (s *session) close() {
	s.conn.Object(secretsName, s.path).Call("org.freedesktop.Secret.Session.Close", 0)
	s.conn.Close()
}

func attributes(account string) map[string]string {
	return map[string]string{"application": Service, "connection": account}
}

// items are the entries under an account, unlocked (the keyring may ask
// for its password).
func (s *session) items(account string) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	if err := s.conn.Object(secretsName, secretsPath).Call(serviceIface+".SearchItems", 0, attributes(account)).Store(&unlocked, &locked); err != nil {
		return nil, err
	}
	if len(locked) > 0 {
		if err := s.unlock(locked); err != nil {
			return nil, err
		}
	}
	return append(unlocked, locked...), nil
}

func (s *session) unlock(paths []dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := s.conn.Object(secretsName, secretsPath).Call(serviceIface+".Unlock", 0, paths).Store(&unlocked, &prompt); err != nil {
		return err
	}
	return s.prompt(prompt)
}

// prompt shows the service's dialog, if it has one, and waits for it.
func (s *session) prompt(path dbus.ObjectPath) error {
	if path == noPrompt || path == "" {
		return nil
	}
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(promptIface), dbus.WithMatchMember("Completed")}
	if err := s.conn.AddMatchSignal(match...); err != nil {
		return err
	}
	defer s.conn.RemoveMatchSignal(match...)
	signals := make(chan *dbus.Signal, 4)
	s.conn.Signal(signals)
	defer s.conn.RemoveSignal(signals)
	if err := s.conn.Object(secretsName, path).Call(promptIface+".Prompt", 0, "").Err; err != nil {
		return err
	}
	deadline := time.After(promptDeadline)
	for {
		select {
		case signal := <-signals:
			if signal.Path != path || signal.Name != promptIface+".Completed" {
				continue
			}
			if dismissed, _ := signal.Body[0].(bool); dismissed {
				return errors.New("keyring: the unlock was dismissed")
			}
			return nil
		case <-deadline:
			return errors.New("keyring: the unlock timed out")
		}
	}
}

func (secretService) Get(account string) (string, error) {
	s, err := open()
	if err != nil {
		return "", err
	}
	defer s.close()
	items, err := s.items(account)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "", ErrNotFound
	}
	var value secret
	if err = s.conn.Object(secretsName, items[0]).Call(itemIface+".GetSecret", 0, s.path).Store(&value); err != nil {
		return "", err
	}
	return string(value.Value), nil
}

func (secretService) Set(account, key string) error {
	s, err := open()
	if err != nil {
		return err
	}
	defer s.close()
	properties := map[string]dbus.Variant{
		itemIface + ".Label":      dbus.MakeVariant(Service + " API key"),
		itemIface + ".Attributes": dbus.MakeVariant(attributes(account)),
	}
	value := secret{Session: s.path, Value: []byte(key), ContentType: "text/plain; charset=utf8"}
	// The default collection, or "login" where no alias is set.
	for _, collection := range []dbus.ObjectPath{defaultAlias, loginPath} {
		if err = s.unlock([]dbus.ObjectPath{collection}); err != nil {
			continue
		}
		var item, prompt dbus.ObjectPath
		if err = s.conn.Object(secretsName, collection).Call("org.freedesktop.Secret.Collection.CreateItem", 0, properties, value, true).Store(&item, &prompt); err != nil {
			continue
		}
		return s.prompt(prompt)
	}
	return err
}

func (secretService) Delete(account string) error {
	s, err := open()
	if err != nil {
		return err
	}
	defer s.close()
	items, err := s.items(account)
	if err != nil {
		return err
	}
	for _, item := range items {
		var prompt dbus.ObjectPath
		if err = s.conn.Object(secretsName, item).Call(itemIface+".Delete", 0).Store(&prompt); err != nil {
			return err
		}
		if err = s.prompt(prompt); err != nil {
			return err
		}
	}
	return nil
}
