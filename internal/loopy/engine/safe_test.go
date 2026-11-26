// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"1loop/internal/loopy"
)

func init() { safeTime, safeMemory = 1, 1<<10 } // quick in the tests

const testMaster = "cheval correct agrafe pile"

func TestSafe(t *testing.T) {
	dir := t.TempDir()
	s, err := openSafe(dir)
	if err != nil || s.State() != "none" {
		t.Fatal(s.State(), err)
	}
	if _, err := s.put(loopy.VaultEntry{Name: "x"}, map[string]string{"key": "v"}); err != errNoSafe {
		t.Error(err)
	}
	for _, weak := range []string{"", "court", "aaaaaaaaaaaaaaaa"} {
		if s.Create(weak, nil) == nil {
			t.Errorf("%q was taken", weak)
		}
	}
	if err := s.Create(testMaster, nil); err != nil {
		t.Fatal(err)
	}
	if s.Create(testMaster, nil) == nil {
		t.Error("made twice")
	}
	entry, err := s.put(loopy.VaultEntry{Kind: "signin", Site: "example.com", Name: "Exemple"}, map[string]string{"user": "moi@example.com", "password": "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vault.safe"} {
		data, _ := os.ReadFile(filepath.Join(dir, name))
		if len(data) == 0 || bytes.Contains(data, []byte("horse")) || bytes.Contains(data, []byte("example")) || bytes.Contains(data, []byte("cheval")) {
			t.Errorf("%s holds something in clear", name)
		}
		if info, _ := os.Stat(filepath.Join(dir, name)); info.Mode().Perm()&0077 != 0 && os.PathSeparator == '/' {
			t.Errorf("others may read %s: %v", name, info.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "vault.key")); err == nil {
		t.Error("a key was kept on the machine")
	}
	for host, want := range map[string]int{"https://example.com/login": 1, "www.example.com": 1, "accounts.example.com": 1, "example.com.evil.org": 0, "notexample.com": 0, "com": 0} {
		if got, _ := s.forSite(host); len(got) != want {
			t.Errorf("%s: %d", host, len(got))
		}
	}
	// Another start of 1loop: locked, nothing to see.
	s, err = openSafe(dir)
	if err != nil || s.State() != "locked" || len(s.list()) != 0 || len(s.secrets()) != 0 {
		t.Fatal(s.State(), err, s.list())
	}
	if _, err := s.get(entry.ID); err != errLocked {
		t.Error(err)
	}
	if err := s.Unlock("pas le bon mot de passe"); err == nil || s.State() != "locked" {
		t.Error("a wrong master password opened it")
	}
	if err := s.Unlock(testMaster); err != nil {
		t.Fatal(err)
	}
	if got, err := s.get(entry.ID); err != nil || got.Values["password"] != "correct horse" {
		t.Error(got, err)
	}
	// A new master password: the old one no longer opens.
	if s.Change("faux mot de passe", "un autre mot de passe") == nil {
		t.Error("changed without the old one")
	}
	if err := s.Change(testMaster, "un autre mot de passe"); err != nil {
		t.Fatal(err)
	}
	s, _ = openSafe(dir)
	if s.Unlock(testMaster) == nil {
		t.Error("the old one still opens")
	}
	if err := s.Unlock("un autre mot de passe"); err != nil {
		t.Error(err)
	}
	s.Lock()
	if s.State() != "locked" || len(s.secrets()) != 0 {
		t.Error("not locked")
	}
	// Damaged: refused, not replaced.
	os.WriteFile(filepath.Join(dir, "vault.safe"), []byte("{"), 0600)
	if _, err := openSafe(dir); err == nil {
		t.Error("damaged and opened")
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "vault.safe")); string(data) != "{" {
		t.Error("replaced")
	}
}

func TestSafeSlowsGuesses(t *testing.T) {
	s, _ := openSafe(t.TempDir())
	s.Create(testMaster, nil)
	s.Lock()
	now := time.Now()
	s.now = func() time.Time { return now }
	for range 4 {
		s.Unlock("pas le bon mot")
	}
	if err := s.Unlock(testMaster); err == nil || !strings.Contains(err.Error(), "wait") {
		t.Errorf("no wait: %v", err)
	}
	now = now.Add(10 * time.Second)
	if err := s.Unlock(testMaster); err != nil {
		t.Error(err)
	}
}

func TestSafeLocksAndStays(t *testing.T) {
	dir := t.TempDir()
	s, _ := openSafe(dir)
	s.Create(testMaster, nil)
	now := time.Now()
	s.now = func() time.Time { return now }
	s.put(loopy.VaultEntry{Kind: "key", Name: "API"}, map[string]string{"key": "abc-1234"})
	now = now.Add(14 * time.Minute)
	if s.Expire(15 * time.Minute) {
		t.Error("too soon")
	}
	now = now.Add(2 * time.Minute)
	if s.Expire(0) || !s.Expire(15*time.Minute) || s.State() != "locked" {
		t.Error("left open")
	}
	if s.Stay(true, "pas le bon mot") == nil {
		t.Error("kept open without the master password")
	}
	if err := s.Stay(true, testMaster); err != nil {
		t.Fatal(err)
	}
	s, _ = openSafe(dir)
	if s.State() != "unlocked" || !s.Stays() || len(s.secrets()) != 1 {
		t.Error("it did not open by itself")
	}
	if err := s.Stay(false, ""); err != nil {
		t.Fatal(err)
	}
	s, _ = openSafe(dir)
	if s.State() != "locked" {
		t.Error("it still opens by itself")
	}
	// The machine's key alone, without what it opened, opens nothing.
	if s.Unlock(testMaster) != nil {
		t.Error("the master password no longer opens")
	}
}

// RFC 6238's own examples.
func TestTOTP(t *testing.T) {
	seed := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	for at, want := range map[int64]string{59: "94287082", 1111111109: "07081804", 1234567890: "89005924", 20000000000: "65353130"} {
		if got, err := totp("otpauth://totp/x?digits=8&secret="+seed, time.Unix(at, 0)); err != nil || got != want {
			t.Errorf("%d: %s %v", at, got, err)
		}
		if got, _ := totp("gezd gnbv gy3t qojq gezd gnbv gy3t qojq", time.Unix(at, 0)); got != want[2:] {
			t.Errorf("%d: %s", at, got)
		}
	}
	if got, _ := totp("otpauth://totp/x?algorithm=SHA256&digits=8&secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQGEZA", time.Unix(59, 0)); got != "46119246" {
		t.Error(got)
	}
	if _, err := totp("pas du base32 !", time.Now()); err == nil {
		t.Error("taken")
	}
}

func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		p, err := newPassword(20)
		if err != nil || len(p) != 20 || seen[p] {
			t.Fatal(p, err)
		}
		seen[p] = true
	}
}

// What an earlier version kept moves behind the master password; the
// accounts' passwords, which nobody is there to open, stay.
func TestVaultMovesIn(t *testing.T) {
	dir := t.TempDir()
	e, _ := wake(t, dir, func([]heard) any { return "ok" })
	e.vault.put(loopy.VaultEntry{Kind: "signin", Site: "example.com", Name: "Exemple"}, map[string]string{"user": "moi", "password": "correct horse"})
	e.vault.put(loopy.VaultEntry{ID: "account-a", Kind: "account"}, map[string]string{"password": "mail pass"})
	if err := e.Do(t.Context(), loopy.Action{Do: "vault.create", Values: map[string]string{"master": testMaster}}); err != nil {
		t.Fatal(err)
	}
	if list := e.safe.list(); len(list) != 1 || list[0].Name != "Exemple" {
		t.Errorf("%+v", list)
	}
	if len(e.vault.users()) != 0 || len(e.vault.entries) != 1 {
		t.Errorf("%+v", e.vault.entries)
	}
	view := e.View()
	data, _ := json.Marshal(view)
	if view.VaultState != "unlocked" || strings.Contains(string(data), "horse") {
		t.Error("the view holds a value")
	}
	// Another entry, changed, a field taken out.
	id := view.Vault[0].ID
	if err := e.SaveSecret(loopy.VaultEntry{ID: id, Kind: "signin", Site: "https://www.example.com/x", Name: "Exemple"}, map[string]string{"password": "", "totp": "GEZDGNBVGY3TQOJQ"}); err != nil {
		t.Fatal(err)
	}
	values, err := e.RevealSecret(id)
	if err != nil || values["password"] != "correct horse" || len(values["code"]) != 6 || values["user"] != "moi" {
		t.Error(values, err)
	}
	if err := e.SaveSecret(loopy.VaultEntry{ID: id, Kind: "signin", Site: "example.com", Name: "Exemple"}, map[string]string{"totp": "-"}); err != nil {
		t.Fatal(err)
	}
	if values, _ := e.RevealSecret(id); values["totp"] != "" || values["password"] == "" {
		t.Error(values)
	}
	if e.SaveSecret(loopy.VaultEntry{Kind: "key", Name: "exemple"}, map[string]string{"key": "x"}) == nil {
		t.Error("two entries of one name")
	}
	e.Do(t.Context(), loopy.Action{Do: "vault.lock"})
	if _, err := e.RevealSecret(id); err != errLocked {
		t.Error(err)
	}
	if err := e.Do(t.Context(), loopy.Action{Do: "vault.unlock", Values: map[string]string{"master": "mauvais mot"}}); err == nil {
		t.Error("opened")
	}
}

// The master password goes to the vault and nowhere else.
func TestMasterIsKeptNowhere(t *testing.T) {
	dir := t.TempDir()
	var asked string
	e, _ := wake(t, dir, func(messages []heard) any {
		for _, m := range messages {
			asked += m.text()
		}
		if m := messages[len(messages)-1]; m.Role == "user" && !strings.Contains(m.text(), "[") {
			return "ok"
		}
		return "ok"
	})
	e.CreateVault(testMaster)
	e.LockVault()
	card := unlockCard("pour voir")
	message := e.add(loopy.Message{From: "loopy", Card: &card})
	if e.Answer(loopy.Answer{Message: message.ID, Answer: "Unlock", Values: map[string]string{"master": "mauvais mot de passe"}}) == nil {
		t.Fatal("opened")
	}
	if got, _ := e.message(message.ID); !got.Card.Waiting() {
		t.Error("the card stopped waiting after a wrong one")
	}
	if err := e.Answer(loopy.Answer{Message: message.ID, Answer: "Unlock", Values: map[string]string{"master": testMaster}}); err != nil {
		t.Fatal(err)
	}
	if e.safe.State() != "unlocked" {
		t.Error("still locked")
	}
	e.Send("salut", nil)
	until(t, "an answer", func() bool { return !e.Busy() })
	e.Close()
	filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		if info != nil && info.Mode().IsRegular() {
			if data, _ := os.ReadFile(path); bytes.Contains(data, []byte(testMaster)) {
				t.Errorf("%s holds it", path)
			}
		}
		return nil
	})
	if strings.Contains(asked, testMaster) {
		t.Error("the model was given it")
	}
}
