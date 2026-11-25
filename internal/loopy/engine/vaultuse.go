// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"1loop/internal/loopy"
)

// withoutMaster are the values of an answer, less the master password: it
// went to the safe, and nothing else may hold it.
func withoutMaster(values map[string]string) map[string]string {
	if _, ok := values["master"]; !ok {
		return values
	}
	out := maps.Clone(values)
	delete(out, "master")
	return out
}

// CreateVault makes the safe; what an earlier version kept moves in.
func (e *Engine) CreateVault(master string) error {
	if e.safe == nil {
		return errors.New("the vault cannot be read")
	}
	var moved []secretEntry
	if e.vault != nil {
		moved = e.vault.users()
	}
	if err := e.safe.Create(master, moved); err != nil {
		return err
	}
	for _, entry := range moved {
		e.vault.remove(entry.ID)
	}
	e.log("The user made their vault.")
	e.tell(nil, nil, true)
	return nil
}

// UnlockVault opens the safe, and lets go what waited for it.
func (e *Engine) UnlockVault(master string) error {
	if e.safe == nil {
		return errors.New("the vault cannot be read")
	}
	if err := e.safe.Unlock(master); err != nil {
		return err
	}
	for _, id := range e.waiting() { // a card that asked for just that
		message, ok := e.message(id)
		e.mu.Lock()
		waits := e.waits[id] != nil
		e.mu.Unlock()
		if ok && message.Card.Kind == "unlock" && !waits {
			e.close(id, message.Card.Actions[0], "")
		}
	}
	e.tell(nil, nil, true)
	e.resumeLinks()
	return nil
}

// LockVault forgets what is open.
func (e *Engine) LockVault() error {
	if e.safe == nil {
		return errors.New("the vault cannot be read")
	}
	e.safe.Lock()
	e.tell(nil, nil, true)
	return nil
}

func (e *Engine) changeSafe(change func(*safe) error) error {
	if e.safe == nil {
		return errors.New("the vault cannot be read")
	}
	err := change(e.safe)
	e.tell(nil, nil, true)
	return err
}

// openVaultFor has the user open the safe when it is locked.
func (e *Engine) openVaultFor(ctx context.Context, why string) error {
	if e.safe == nil {
		return errors.New("the vault cannot be read")
	}
	switch e.safe.State() {
	case "none":
		return errNoSafe
	case "unlocked":
		return nil
	}
	answer, err := e.ask(ctx, unlockCard(why))
	if err != nil {
		return err
	}
	if answer.Answer != "Unlock" {
		return errors.New("the user left the vault locked")
	}
	return nil
}

func unlockCard(why string) loopy.Card {
	return loopy.Card{Kind: "unlock", Title: "Open your vault", Body: why, Subtitle: "Loopy never sees what you type here", Fields: []loopy.Field{{Label: "master"}}, Secret: true, Actions: []string{"Unlock", "Not now"}}
}

// signInValues are what a sign-in at host is filled with: the vault's, or
// what the user types in a form of 1loop's.
func (e *Engine) signInValues(ctx context.Context, args toolArgs, host string) (map[string]string, error) {
	state := "none"
	if e.safe != nil {
		state = e.safe.State()
	}
	if state == "locked" {
		if err := e.openVaultFor(ctx, "To sign in to "+host+". "+args.Why); err == nil {
			state = "unlocked"
		} else if ctx.Err() != nil {
			return nil, err
		}
	}
	if state == "unlocked" {
		entries, err := e.safe.forSite(host)
		if err != nil {
			return nil, err
		}
		if args.Entry != "" {
			entries = slices.DeleteFunc(entries, func(s secretEntry) bool { return !strings.EqualFold(s.Name, args.Entry) })
		}
		if len(entries) > 0 {
			entry := entries[0]
			if !e.rule("signin", host) {
				answer, err := e.ask(ctx, loopy.Card{Kind: "approval", Title: "Sign in to " + host, Subtitle: "with your vault's " + entry.Name, Body: args.Why, Warning: "Filled directly on " + host + ": Loopy does not see it",
					Actions: []string{"Allow once", "Always allow on " + host, "Deny"}})
				if err != nil {
					return nil, err
				}
				if answer.Answer == "Deny" {
					return nil, errors.New("the user said no. Do not try again before their next message")
				}
				if strings.HasPrefix(answer.Answer, "Always") {
					e.addRule("signin", host)
				}
			}
			return entry.filled(e.now())
		}
	}
	if args.UserField == "" && args.PasswordField == "" {
		return nil, errors.New("the vault has no two-step secret for " + host + ": show a needs_you card, with ask, for the code")
	}
	var fields []loopy.Field
	if args.UserField != "" {
		fields = append(fields, loopy.Field{Label: "user"})
	}
	if args.PasswordField != "" {
		fields = append(fields, loopy.Field{Label: "password"})
	}
	answer, err := e.ask(ctx, loopy.Card{Kind: "signin", Title: "Sign in to " + host, Body: args.Why, Subtitle: "Filled directly on " + host, Site: host, Fields: fields, Keep: state == "unlocked", Actions: []string{"Sign in", "Not now"}})
	if err != nil {
		return nil, err
	}
	if answer.Answer != "Sign in" {
		return nil, errors.New("the user does not want to sign in now")
	}
	if answer.Keep && state == "unlocked" && len(answer.Values) > 0 {
		if _, err := e.safe.put(loopy.VaultEntry{Kind: "signin", Site: host, Name: host, Hint: answer.Hint}, answer.Values); err != nil {
			e.add(loopy.Message{From: "loopy", Note: "The vault could not keep it: " + err.Error()})
		}
		e.tell(nil, nil, true)
	}
	return answer.Values, nil
}

// SaveSecret keeps an entry of the vault.
func (e *Engine) SaveSecret(entry loopy.VaultEntry, values map[string]string) error {
	if e.safe == nil {
		return errors.New("the vault cannot be read")
	}
	if entry.Kind != "signin" && entry.Kind != "key" && entry.Kind != "contact" {
		return errors.New("unknown kind of entry")
	}
	entry.Name = strings.TrimSpace(entry.Name)
	if entry.Name == "" {
		return errors.New("give it a name")
	}
	values = maps.Clone(values)
	if values == nil {
		values = map[string]string{}
	}
	if entry.Kind == "signin" {
		entry.Site = site(entry.Site)
		if !strings.Contains(entry.Site, ".") && entry.Site != "localhost" {
			return errors.New("name the site, as example.com")
		}
	}
	if entry.ID != "" {
		old, err := e.safe.get(entry.ID)
		if err != nil {
			return err
		}
		for name, value := range old.Values { // an empty field keeps its value
			if values[name] == "" {
				values[name] = value
			}
		}
	}
	for name, value := range values {
		if value == "" || value == erased { // a dash takes it out
			delete(values, name)
		}
	}
	if len(values) == 0 {
		return errors.New("give it a value")
	}
	if values["totp"] != "" {
		if _, err := totp(values["totp"], e.now()); err != nil {
			return errors.New("the two-step secret: " + err.Error())
		}
	}
	for _, other := range e.safe.list() {
		if other.ID != entry.ID && strings.EqualFold(other.Name, entry.Name) {
			return errors.New("another entry has that name")
		}
	}
	_, err := e.safe.put(entry, values)
	e.tell(nil, nil, true)
	return err
}

const erased = "-"

// RemoveSecret takes an entry out of the vault.
func (e *Engine) RemoveSecret(id string) error {
	if e.safe == nil {
		return errors.New("the vault cannot be read")
	}
	err := e.safe.remove(id)
	e.tell(nil, nil, true)
	return err
}

// RevealSecret is an entry's values for the user's eyes, its code of the
// moment beside them; a new password for no id. Loopy's tools never call it.
func (e *Engine) RevealSecret(id string) (map[string]string, error) {
	if id == "" {
		password, err := newPassword(20)
		return map[string]string{"password": password}, err
	}
	if e.safe == nil {
		return nil, errors.New("the vault cannot be read")
	}
	entry, err := e.safe.get(id)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(entry.Values)
	if out["totp"] != "" {
		if code, err := totp(out["totp"], e.now()); err == nil {
			out["code"] = code
		}
	}
	return out, nil
}

// lockSoon locks a safe left open too long.
func (e *Engine) lockSoon() {
	if e.safe == nil {
		return
	}
	e.store.Lock()
	minutes := e.store.Settings.VaultLock
	e.store.Unlock()
	if e.safe.Expire(time.Duration(minutes) * time.Minute) {
		e.tell(nil, nil, true)
	}
}
