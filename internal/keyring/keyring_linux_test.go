// SPDX-License-Identifier: MPL-2.0

//go:build linux && !android

package keyring

import (
	"bufio"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

// fakeService is a Secret Service on a private bus: just what keyring
// calls, with an unlock prompt for its locked items.
type fakeService struct {
	conn   *dbus.Conn
	mu     sync.Mutex
	items  map[dbus.ObjectPath]*fakeItem
	next   int
	locked bool // new items start locked: reading them needs the prompt
	refuse bool // the prompt is dismissed
}

type fakeItem struct {
	service    *fakeService
	path       dbus.ObjectPath
	attributes map[string]string
	value      []byte
	locked     bool
}

func (f *fakeService) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != "plain" {
		return dbus.Variant{}, "", dbus.NewError("org.freedesktop.DBus.Error.NotSupported", nil)
	}
	return dbus.MakeVariant(""), "/org/freedesktop/secrets/session/1", nil
}

func matches(item, query map[string]string) bool {
	for key, value := range query {
		if item[key] != value {
			return false
		}
	}
	return true
}

func (f *fakeService) SearchItems(query map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var unlocked, locked []dbus.ObjectPath
	for path, item := range f.items {
		if !matches(item.attributes, query) {
			continue
		}
		if item.locked {
			locked = append(locked, path)
		} else {
			unlocked = append(unlocked, path)
		}
	}
	return unlocked, locked, nil
}

func (f *fakeService) Unlock(paths []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var waiting []dbus.ObjectPath
	for _, path := range paths {
		if item := f.items[path]; item != nil && item.locked {
			waiting = append(waiting, path)
		}
	}
	if len(waiting) == 0 {
		return paths, "/", nil
	}
	f.next++
	prompt := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/prompt/p%d", f.next))
	f.conn.Export(&fakePrompt{service: f, path: prompt, unlock: waiting}, prompt, promptIface)
	return nil, prompt, nil
}

func (f *fakeService) CreateItem(properties map[string]dbus.Variant, value secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	attributes, _ := properties[itemIface+".Attributes"].Value().(map[string]string)
	f.mu.Lock()
	defer f.mu.Unlock()
	if replace {
		for _, item := range f.items {
			if matches(item.attributes, attributes) && len(item.attributes) == len(attributes) {
				item.value = value.Value
				return item.path, "/", nil
			}
		}
	}
	f.next++
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/collection/login/i%d", f.next))
	item := &fakeItem{service: f, path: path, attributes: attributes, value: value.Value, locked: f.locked}
	f.items[path] = item
	f.conn.Export(item, path, itemIface)
	return path, "/", nil
}

func (item *fakeItem) GetSecret(session dbus.ObjectPath) (secret, *dbus.Error) {
	item.service.mu.Lock()
	defer item.service.mu.Unlock()
	if item.locked {
		return secret{}, dbus.NewError("org.freedesktop.Secret.Error.IsLocked", nil)
	}
	return secret{Session: session, Value: item.value, ContentType: "text/plain"}, nil
}

func (item *fakeItem) Delete() (dbus.ObjectPath, *dbus.Error) {
	item.service.mu.Lock()
	defer item.service.mu.Unlock()
	delete(item.service.items, item.path)
	return "/", nil
}

type fakePrompt struct {
	service *fakeService
	path    dbus.ObjectPath
	unlock  []dbus.ObjectPath
}

func (p *fakePrompt) Prompt(window string) *dbus.Error {
	p.service.mu.Lock()
	dismissed := p.service.refuse
	if !dismissed {
		for _, path := range p.unlock {
			if item := p.service.items[path]; item != nil {
				item.locked = false
			}
		}
	}
	p.service.mu.Unlock()
	go p.service.conn.Emit(p.path, promptIface+".Completed", dismissed, dbus.MakeVariant(p.unlock))
	return nil
}

type fakeCollection struct{ *fakeService }

type fakeSession struct{}

func (fakeSession) Close() *dbus.Error { return nil }

// privateBus starts a session bus of the test's own and points
// DBUS_SESSION_BUS_ADDRESS at it.
func privateBus(t *testing.T) {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon")
	}
	command := exec.Command(daemon, "--session", "--nofork", "--print-address=1")
	out, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	address, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", strings.TrimSpace(address))
}

func startFake(t *testing.T) *fakeService {
	t.Helper()
	privateBus(t)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	service := &fakeService{conn: conn, items: map[dbus.ObjectPath]*fakeItem{}}
	conn.Export(service, secretsPath, serviceIface)
	conn.Export(fakeCollection{service}, defaultAlias, "org.freedesktop.Secret.Collection")
	conn.Export(fakeSession{}, "/org/freedesktop/secrets/session/1", "org.freedesktop.Secret.Session")
	if reply, err := conn.RequestName(secretsName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("name: %v %v", reply, err)
	}
	return service
}

func TestSecretServiceRoundTrip(t *testing.T) {
	startFake(t)
	store := System()
	if _, err := store.Get("conn-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get before Set: %v", err)
	}
	if err := store.Set("conn-1", "sk-one"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("conn-1", "sk-two"); err != nil { // a changed key replaces the old one
		t.Fatal(err)
	}
	if err := store.Set("conn-2", "sk-other"); err != nil {
		t.Fatal(err)
	}
	if key, err := store.Get("conn-1"); err != nil || key != "sk-two" {
		t.Fatalf("Get = %q, %v", key, err)
	}
	if err := store.Delete("conn-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("conn-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: %v", err)
	}
	if key, _ := store.Get("conn-2"); key != "sk-other" {
		t.Fatalf("the other connection's key: %q", key)
	}
	if err := store.Delete("conn-1"); err != nil {
		t.Fatalf("Delete of nothing: %v", err)
	}
}

func TestSecretServiceUnlockPrompt(t *testing.T) {
	service := startFake(t)
	service.locked = true
	store := System()
	if err := store.Set("conn-1", "sk-locked"); err != nil {
		t.Fatal(err)
	}
	service.refuse = true
	if _, err := store.Get("conn-1"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("a dismissed unlock: %v", err)
	}
	service.refuse = false
	if key, err := store.Get("conn-1"); err != nil || key != "sk-locked" {
		t.Fatalf("Get after the prompt = %q, %v", key, err)
	}
}

func TestSecretServiceMissing(t *testing.T) {
	privateBus(t) // a bus without the service
	if _, err := System().Get("conn-1"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Get = %v, want ErrUnavailable", err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/bus")
	if err := System().Set("conn-1", "sk"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Set without a bus = %v", err)
	}
}
