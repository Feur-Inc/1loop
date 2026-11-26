// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"1loop/internal/agent"
	"1loop/internal/keyring"
	"1loop/internal/platform"
)

// The API keys of the connections (#510): typed on a connection's page,
// kept in ui.keys while 1loop runs and in the system's secret store, by
// connection ID, so that they come back at the next start. Where there is
// no store, or it refuses, they stay in memory only, as before; no key is
// ever written to a file. ui.keyed remembers which connections had one, to
// say so when it is gone.

// noKeyStore is the connection page's line where keys do not outlive 1loop.
const noKeyStore = "No secret store here: the key is kept until 1loop closes."

// keyNeeded is what a connection whose key was lost says.
const keyNeeded = "Needs its API key again"

// keyVault writes to the store off the interface's thread (a locked
// keyring asks for its password), one call at a time, in order.
type keyVault struct {
	store   keyring.Store
	mu      sync.Mutex
	last    chan struct{} // closed once the last write queued is done
	refused atomic.Bool   // the store's last answer was a failure
}

func newKeyVault(store keyring.Store) *keyVault { return &keyVault{store: store} }

// kept tells whether keys outlive 1loop here.
func (vault *keyVault) kept() bool {
	return vault != nil && vault.store != nil && !vault.refused.Load()
}

func (vault *keyVault) note(err error) {
	failed := err != nil && !errors.Is(err, keyring.ErrNotFound)
	if failed {
		platform.Devf("ui", "secret store: %v", err)
	}
	vault.refused.Store(failed)
}

// put keeps a connection's key, or forgets it when empty.
func (vault *keyVault) put(id, key string) {
	if vault == nil || vault.store == nil {
		return
	}
	vault.mu.Lock()
	previous, done := vault.last, make(chan struct{})
	vault.last = done
	vault.mu.Unlock()
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		if key == "" {
			vault.note(vault.store.Delete(id))
		} else {
			vault.note(vault.store.Set(id, key))
		}
	}()
}

// wait returns once the writes queued so far are done.
func (vault *keyVault) wait() {
	vault.mu.Lock()
	last := vault.last
	vault.mu.Unlock()
	if last != nil {
		<-last
	}
}

// load reads these connections' keys from the store; it blocks. With none
// to read it still asks once, to learn whether there is a store.
func (vault *keyVault) load(ids []string) map[string]string {
	keys := map[string]string{}
	if vault == nil || vault.store == nil {
		return keys
	}
	if len(ids) == 0 {
		_, err := vault.store.Get("probe")
		vault.note(err)
		return keys
	}
	var failure error
	for _, id := range ids {
		key, err := vault.store.Get(id)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			failure = err
			if errors.Is(err, keyring.ErrUnavailable) {
				break
			}
			continue
		}
		if key != "" {
			keys[id] = key
		}
	}
	vault.note(failure)
	return keys
}

// keyMissing tells whether a connection had a key that 1loop no longer has.
func (app *desktopApp) keyMissing(id string) bool {
	return app.keyed[id] && app.keys[id] == ""
}

// setKey is a key typed on a connection's page.
func (app *desktopApp) setKey(id, key string) {
	if app.keys[id] == key && app.keyed[id] == (key != "") {
		return
	}
	if app.keyed == nil {
		app.keyed = map[string]bool{}
	}
	if key == "" {
		delete(app.keys, id)
		delete(app.keyed, id)
	} else {
		app.keys[id], app.keyed[id] = key, true
	}
	app.vault.put(id, key)
}

// keyedList is ui.keyed for the state file, without removed connections.
func (app *desktopApp) keyedList() []string {
	var ids []string
	for _, conn := range app.connectionsList {
		if app.keyed[conn.ID] {
			ids = append(ids, conn.ID)
		}
	}
	return ids
}

// loadKeys reads the saved keys in the background, once the state is
// loaded; the models of the connections waiting for theirs load after.
func (ui *desktopUI) loadKeys() {
	ids := ui.keyedList()
	if ui.vault == nil || ui.vault.store == nil {
		return
	}
	ui.keysLoading = len(ids) > 0
	go func() {
		keys := ui.vault.load(ids)
		fyne.Do(func() {
			if !ui.keysLoading {
				return
			}
			ui.keysLoading = false
			for id, key := range keys {
				if _, typed := ui.keys[id]; !typed { // one typed meanwhile wins
					ui.keys[id] = key
				}
			}
			ui.refreshModels()
		})
	}()
}

// keyProblem is why a connection's models are not listed before asking for
// them: its key is lost, or still on its way from the store.
func (ui *desktopUI) keyProblem(conn agent.Connection) string {
	if !ui.keyMissing(conn.ID) {
		return ""
	}
	if ui.keysLoading {
		return "Loading its key…"
	}
	return keyNeeded
}

// keyNeededConnection is the first enabled connection whose key is lost.
func (ui *desktopUI) keyNeededConnection() (agent.Connection, bool) {
	index := slices.IndexFunc(ui.connectionsList, func(conn agent.Connection) bool {
		return conn.Enabled && ui.modelUnavailable[conn.ID] == keyNeeded
	})
	if index < 0 {
		return agent.Connection{}, false
	}
	return ui.connectionsList[index], true
}

// openConnection opens a connection's page in Settings.
func (ui *desktopUI) openConnection(id string) {
	for _, conn := range ui.connectionsList {
		if conn.ID == id {
			ui.prefs.openOn("Models & accounts")
			if ui.prefs.at("Models & accounts") {
				ui.connectionEditor(conn)
			}
			return
		}
	}
}

// keyNeededNote is the model picker's line for a connection whose key is
// lost, with a button to its page.
func (ui *desktopUI) keyNeededNote(id string, closeMenu func()) fyne.CanvasObject {
	name := "This connection"
	for _, conn := range ui.connectionsList {
		if conn.ID == id {
			name = conn.Name
		}
	}
	add := newRow("Add the key", nil, styleOutline, func() {
		closeMenu()
		ui.openConnection(id)
	})
	return container.NewVBox(note(name+" needs its API key again"), container.NewHBox(add))
}
