// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"fyne.io/fyne/v2/test"

	"1loop/internal/agent"
	"1loop/internal/keyring"
)

// memoryStore is a secret store in a map; fail makes it refuse every call.
type memoryStore struct {
	mu      sync.Mutex
	secrets map[string]string
	fail    error
}

func (store *memoryStore) Get(account string) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.fail != nil {
		return "", store.fail
	}
	secret, ok := store.secrets[account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

func (store *memoryStore) Set(account, secret string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.fail != nil {
		return store.fail
	}
	store.secrets[account] = secret
	return nil
}

func (store *memoryStore) Delete(account string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.fail != nil {
		return store.fail
	}
	delete(store.secrets, account)
	return nil
}

func keyApp(store keyring.Store) *desktopApp {
	return &desktopApp{keys: map[string]string{}, keyed: map[string]bool{}, vault: newKeyVault(store),
		connectionsList: []agent.Connection{{ID: "c1", Name: "Mock", Provider: "compatible", Enabled: true}, {ID: "c2", Name: "Other", Provider: "openrouter", Enabled: true}}}
}

func TestConnectionKeysRoundTrip(t *testing.T) {
	store := &memoryStore{secrets: map[string]string{}}
	app := keyApp(store)
	app.setKey("c1", "sk-first")
	app.setKey("c1", "sk-changed") // changing the key updates it
	app.setKey("c2", "sk-other")
	app.vault.wait()
	if store.secrets["c1"] != "sk-changed" || store.secrets["c2"] != "sk-other" {
		t.Fatalf("store = %v", store.secrets)
	}
	if !app.vault.kept() {
		t.Fatal("a working store keeps the keys")
	}
	// The next start: the state file names the connections, the store has the keys.
	next := keyApp(store)
	for _, id := range app.keyedList() {
		next.keyed[id] = true
	}
	loaded := next.vault.load(next.keyedList())
	if loaded["c1"] != "sk-changed" || loaded["c2"] != "sk-other" {
		t.Fatalf("loaded = %v", loaded)
	}
}

func TestConnectionKeyRemovalDeletesIt(t *testing.T) {
	store := &memoryStore{secrets: map[string]string{}}
	app := keyApp(store)
	app.setKey("c1", "sk-first")
	app.setKey("c1", "") // the connection is removed (removeConnection)
	app.vault.wait()
	if _, ok := store.secrets["c1"]; ok {
		t.Fatalf("the key is still in the store: %v", store.secrets)
	}
	if app.keyMissing("c1") || slices.Contains(app.keyedList(), "c1") {
		t.Fatal("a removed connection does not wait for its key")
	}
}

func TestConnectionKeysFallback(t *testing.T) {
	for name, store := range map[string]keyring.Store{
		"no store": nil,
		"refused":  &memoryStore{secrets: map[string]string{}, fail: errors.New("the unlock was dismissed")},
		"no bus":   &memoryStore{secrets: map[string]string{}, fail: keyring.ErrUnavailable},
	} {
		app := keyApp(store)
		app.keyed["c1"] = true
		if keys := app.vault.load([]string{"c1"}); len(keys) != 0 {
			t.Errorf("%s: loaded %v", name, keys)
		}
		app.setKey("c2", "sk-memory")
		app.vault.wait()
		if app.vault.kept() {
			t.Errorf("%s: the page says the key is kept", name)
		}
		if app.keys["c2"] != "sk-memory" {
			t.Errorf("%s: the key is not kept in memory", name)
		}
	}
	// A store that has nothing yet still keeps the keys.
	app := keyApp(&memoryStore{secrets: map[string]string{}})
	app.vault.load(nil)
	if !app.vault.kept() {
		t.Error("an empty store is a store")
	}
}

func TestConnectionKeyMissingNotice(t *testing.T) {
	test.NewTempApp(t)
	ui := &desktopUI{desktopApp: keyApp(&memoryStore{secrets: map[string]string{}})}
	ui.keyed["c1"] = true // it had a key before the restart
	ui.modelUnavailable = map[string]string{}
	ui.keysLoading = true
	if problem := ui.keyProblem(ui.connectionsList[0]); problem == keyNeeded {
		t.Fatal("the key is said lost while the store is still being read")
	}
	ui.keysLoading = false
	if problem := ui.keyProblem(ui.connectionsList[0]); problem != keyNeeded {
		t.Fatalf("problem = %q", problem)
	}
	if problem := ui.keyProblem(ui.connectionsList[1]); problem != "" {
		t.Fatalf("a connection that never had a key: %q", problem)
	}
	ui.modelUnavailable["c1"] = keyNeeded
	if conn, ok := ui.keyNeededConnection(); !ok || conn.ID != "c1" {
		t.Fatalf("keyNeededConnection = %v, %v", conn, ok)
	}
	rows := strings.Join(visibleTexts(ui.connectionRows()), "\n")
	if !strings.Contains(rows, "Mock") || !strings.Contains(rows, keyNeeded) {
		t.Fatalf("the connection row does not say it:\n%s", rows)
	}
	if strings.Count(rows, keyNeeded) != 1 {
		t.Fatalf("only Mock lost its key:\n%s", rows)
	}
	if tapRowNamed(ui.keyNeededNote("c1", func() {}), "Add the key") == nil {
		t.Fatal("the model picker's note has no button to the connection")
	}
	ui.setKey("c1", "sk-again")
	if ui.keyProblem(ui.connectionsList[0]) != "" {
		t.Fatal("the key typed again is still said lost")
	}
}
