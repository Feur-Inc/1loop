// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

// The safe is the user's password manager: sign-ins, keys and contact
// details behind a master password of theirs, on this machine only.
//
// The entries are sealed (AES-256-GCM) with a random key, itself sealed
// with one made from the master password (Argon2id, 64 MB). The master
// password is kept nowhere. Unlocked, the key and the entries are in this
// process's memory until it is locked or 1loop closes. The user may also
// have this machine open it by itself (a server whose AutoLinks sign in at
// night): the key is then kept as vault.key is, which is weaker, and said.
//
// Loopy's model never sees a value: 1loop types it in the page, or hands it
// to an AutoLink the user let use it.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"1loop/internal/loopy"
)

// safeFile is what is on the disk.
type safeFile struct {
	Version int    `json:"version"`
	Salt    []byte `json:"salt"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory"` // KiB
	Threads uint8  `json:"threads"`
	Key     []byte `json:"key"`               // sealed with the master password's
	Machine []byte `json:"machine,omitempty"` // sealed with this machine's, when the user wants it
	Data    []byte `json:"data"`
}

type safe struct {
	mu      sync.Mutex
	dir     string
	path    string
	file    *safeFile // nil: none was made
	key     []byte    // nil: locked
	entries []secretEntry
	used    time.Time
	failed  int
	until   time.Time // no try before, after wrong ones
	now     func() time.Time
}

// What the master password is made into a key with.
var safeTime, safeMemory, safeThreads = uint32(3), uint32(64 << 10), uint8(4)

var (
	errLocked = errors.New("the vault is locked")
	errNoSafe = errors.New("the vault has no master password yet: the user makes one in Settings → Loopy → Vault")
)

// openSafe reads the safe kept in dir, opened when this machine may.
func openSafe(dir string) (*safe, error) {
	s := &safe{dir: dir, path: filepath.Join(dir, "vault.safe"), now: time.Now}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var file safeFile
	if err := json.Unmarshal(data, &file); err != nil || file.Version != 1 || len(file.Salt) < 16 || file.Memory > 1<<20 || file.Time > 64 || file.Time == 0 || file.Threads == 0 {
		return nil, errors.New("Loopy's vault is damaged: it was left as it is")
	}
	s.file = &file
	if len(file.Machine) > 0 {
		if machine, err := vaultKey(dir, false); err == nil {
			if key, err := unseal(machine, file.Machine, "machine"); err == nil {
				s.open(key)
			}
		}
	}
	return s, nil
}

func newSeal(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealed(key, clear []byte, what string) ([]byte, error) {
	seal, err := newSeal(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, seal.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return seal.Seal(nonce, nonce, clear, []byte("1loop loopy safe 1 "+what)), nil
}

func unseal(key, data []byte, what string) ([]byte, error) {
	seal, err := newSeal(key)
	if err != nil {
		return nil, err
	}
	if len(data) < seal.NonceSize() {
		return nil, errors.New("damaged")
	}
	return seal.Open(nil, data[:seal.NonceSize()], data[seal.NonceSize():], []byte("1loop loopy safe 1 "+what))
}

func (f *safeFile) derive(master string) []byte {
	return argon2.IDKey([]byte(master), f.Salt, f.Time, f.Memory, f.Threads, 32)
}

// open takes the entries out, with mu held or before anyone else has it.
func (s *safe) open(key []byte) error {
	clear, err := unseal(key, s.file.Data, "entries")
	if err != nil {
		return errors.New("Loopy's vault is damaged")
	}
	var entries []secretEntry
	if err := json.Unmarshal(clear, &entries); err != nil {
		return errors.New("Loopy's vault is damaged")
	}
	s.key, s.entries, s.used = key, entries, s.now()
	return nil
}

// State is "none", "locked" or "unlocked".
func (s *safe) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.file == nil:
		return "none"
	case s.key == nil:
		return "locked"
	}
	return "unlocked"
}

// Stays says whether this machine opens it by itself.
func (s *safe) Stays() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file != nil && len(s.file.Machine) > 0
}

func checkMaster(master string) error {
	if len([]rune(master)) < 10 {
		return errors.New("a master password has at least 10 characters: several words make a good one")
	}
	if len(master) > 1024 {
		return errors.New("that master password is too long")
	}
	first := []rune(master)[0]
	if strings.Trim(master, string(first)) == "" {
		return errors.New("choose a master password that is harder to guess")
	}
	return nil
}

// Create makes the safe, with what an earlier vault held.
func (s *safe) Create(master string, entries []secretEntry) error {
	if err := checkMaster(master); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != nil {
		return errors.New("the vault has a master password already")
	}
	file := &safeFile{Version: 1, Salt: make([]byte, 16), Time: safeTime, Memory: safeMemory, Threads: safeThreads}
	key := make([]byte, 32)
	if _, err := rand.Read(file.Salt); err != nil {
		return err
	}
	if _, err := rand.Read(key); err != nil {
		return err
	}
	var err error
	if file.Key, err = sealed(file.derive(master), key, "key"); err != nil {
		return err
	}
	s.file, s.key, s.entries, s.used = file, key, slices.Clone(entries), s.now()
	if err := s.save(); err != nil {
		s.file, s.key, s.entries = nil, nil, nil
		return err
	}
	return nil
}

// Unlock opens it. Wrong tries wait longer and longer.
func (s *safe) Unlock(master string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unlock(master)
}

func (s *safe) unlock(master string) error {
	if s.file == nil {
		return errNoSafe
	}
	if wait := s.until.Sub(s.now()); wait > 0 {
		return fmt.Errorf("too many wrong tries: wait %d seconds", int(wait.Seconds())+1)
	}
	key, err := unseal(s.file.derive(master), s.file.Key, "key")
	if err != nil {
		if s.failed++; s.failed >= 4 {
			s.until = s.now().Add(min(5*time.Minute, time.Second<<min(s.failed-2, 9)))
		}
		return errors.New("that is not the master password")
	}
	s.failed, s.until = 0, time.Time{}
	if s.key != nil {
		s.used = s.now()
		return nil
	}
	return s.open(key)
}

// Lock forgets the key and the entries.
func (s *safe) Lock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.key)
	s.key, s.entries = nil, nil
}

// Change replaces the master password.
func (s *safe) Change(old, master string) error {
	if err := checkMaster(master); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.unlock(old); err != nil {
		return err
	}
	file := *s.file
	file.Salt, file.Time, file.Memory, file.Threads = make([]byte, 16), safeTime, safeMemory, safeThreads
	if _, err := rand.Read(file.Salt); err != nil {
		return err
	}
	var err error
	if file.Key, err = sealed(file.derive(master), s.key, "key"); err != nil {
		return err
	}
	return s.replace(&file)
}

// Stay has this machine open it by itself, or not any more. It takes the
// master password: an open screen is not enough.
func (s *safe) Stay(on bool, master string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return errNoSafe
	}
	file := *s.file
	if !on {
		file.Machine = nil
		return s.replace(&file)
	}
	if err := s.unlock(master); err != nil {
		return err
	}
	machine, err := vaultKey(s.dir, true)
	if err != nil {
		return err
	}
	if file.Machine, err = sealed(machine, s.key, "machine"); err != nil {
		return err
	}
	return s.replace(&file)
}

func (s *safe) replace(file *safeFile) error {
	old := s.file
	s.file = file
	if err := s.write(); err != nil {
		s.file = old
		return err
	}
	return nil
}

// save seals the entries and writes them, with mu held.
func (s *safe) save() error {
	clear, err := json.Marshal(s.entries)
	if err != nil {
		return err
	}
	if s.file.Data, err = sealed(s.key, clear, "entries"); err != nil {
		return err
	}
	return s.write()
}

func (s *safe) write() error {
	data, err := json.Marshal(s.file)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(s.path+".tmp", s.path)
}

// Expire locks it once unused for so long; 0: never.
func (s *safe) Expire(after time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if after <= 0 || s.key == nil || len(s.file.Machine) > 0 || s.now().Sub(s.used) < after {
		return false
	}
	clear(s.key)
	s.key, s.entries = nil, nil
	return true
}

func (s *safe) ready() error {
	switch {
	case s.file == nil:
		return errNoSafe
	case s.key == nil:
		return errLocked
	}
	s.used = s.now()
	return nil
}

func (s *safe) put(entry loopy.VaultEntry, values map[string]string) (loopy.VaultEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return entry, err
	}
	if entry.ID == "" {
		entry.ID = loopy.NewID()
	}
	entry.Fields = nil
	for name := range values {
		entry.Fields = append(entry.Fields, name)
	}
	sort.Strings(entry.Fields)
	entry.Changed = s.now()
	old := s.entries
	s.entries = append(slices.DeleteFunc(slices.Clone(old), func(e secretEntry) bool { return e.ID == entry.ID }), secretEntry{entry, values})
	if err := s.save(); err != nil {
		s.entries = old
		return entry, err
	}
	return entry, nil
}

func (s *safe) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	old := s.entries
	s.entries = slices.DeleteFunc(slices.Clone(old), func(e secretEntry) bool { return e.ID == id })
	if err := s.save(); err != nil {
		s.entries = old
		return err
	}
	return nil
}

// list is the entries without their values; none when it is locked.
func (s *safe) list() []loopy.VaultEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []loopy.VaultEntry
	for _, e := range s.entries {
		out = append(out, e.VaultEntry)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (s *safe) get(id string) (secretEntry, error) {
	return s.find(func(e secretEntry) bool { return e.ID == id })
}

// named is the entry the user calls so, or for that site.
func (s *safe) named(name string) (secretEntry, error) {
	e, err := s.find(func(e secretEntry) bool { return strings.EqualFold(e.Name, strings.TrimSpace(name)) })
	if err != nil && !errors.Is(err, errLocked) && !errors.Is(err, errNoSafe) && name != "" {
		return s.find(func(e secretEntry) bool { return e.Site != "" && e.Site == site(name) })
	}
	return e, err
}

func (s *safe) find(is func(secretEntry) bool) (secretEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return secretEntry{}, err
	}
	for _, e := range s.entries {
		if is(e) {
			return e, nil
		}
	}
	return secretEntry{}, errors.New("the vault holds no such entry")
}

// forSite are the sign-ins a page at host may get.
func (s *safe) forSite(host string) ([]secretEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	var out []secretEntry
	for _, e := range s.entries {
		if e.Kind == "signin" && sameSite(host, e.Site) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *safe) secrets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.entries {
		for name, value := range e.Values {
			if e.Kind != "contact" && name != "user" && name != "note" && value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

// filled are an entry's values as they are used: the code of the moment
// for a two-step secret, never the secret it comes from.
func (e secretEntry) filled(now time.Time) (map[string]string, error) {
	out := map[string]string{}
	for name, value := range e.Values {
		if name == "totp" {
			code, err := totp(value, now)
			if err != nil {
				return nil, fmt.Errorf("the two-step secret of %s cannot be used: %w", e.Name, err)
			}
			value = code
		}
		out[name] = value
	}
	return out, nil
}

// totp is the code of the moment (RFC 6238) of a secret in base32, or of
// an otpauth:// address.
func totp(secret string, now time.Time) (string, error) {
	digits, period := 6, int64(30)
	var make func() hash.Hash = sha1.New
	if parsed, err := url.Parse(strings.TrimSpace(secret)); err == nil && parsed.Scheme == "otpauth" {
		query := parsed.Query()
		secret = query.Get("secret")
		if n, err := strconv.Atoi(query.Get("digits")); err == nil && n >= 6 && n <= 8 {
			digits = n
		}
		if n, err := strconv.ParseInt(query.Get("period"), 10, 64); err == nil && n > 0 && n <= 300 {
			period = n
		}
		switch strings.ToUpper(query.Get("algorithm")) {
		case "SHA256":
			make = sha256.New
		case "SHA512":
			make = sha512.New
		}
	}
	secret = strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "=", "").Replace(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) == 0 {
		return "", errors.New("it is not a base32 secret or an otpauth:// address")
	}
	mac := hmac.New(make, key)
	binary.Write(mac, binary.BigEndian, now.Unix()/period)
	sum := mac.Sum(nil)
	at := sum[len(sum)-1] & 15
	number := binary.BigEndian.Uint32(sum[at:]) & 0x7fffffff
	modulo := uint32(1)
	for range digits {
		modulo *= 10
	}
	return fmt.Sprintf("%0*d", digits, number%modulo), nil
}

// newPassword is one nobody guesses, that a site takes.
func newPassword(length int) (string, error) {
	const lower, upper, digit, sign = "abcdefghijkmnopqrstuvwxyz", "ABCDEFGHJKLMNPQRSTUVWXYZ", "23456789", "!#%+-=?@_"
	length = min(max(length, 12), 64)
	pick := func(from string) (byte, error) {
		for { // no letter more likely than another
			var b [1]byte
			if _, err := rand.Read(b[:]); err != nil {
				return 0, err
			}
			if int(b[0]) < 256/len(from)*len(from) {
				return from[int(b[0])%len(from)], nil
			}
		}
	}
	for {
		out := []byte{}
		for range length {
			c, err := pick(lower + upper + digit + sign)
			if err != nil {
				return "", err
			}
			out = append(out, c)
		}
		if s := string(out); strings.ContainsAny(s, lower) && strings.ContainsAny(s, upper) && strings.ContainsAny(s, digit) && strings.ContainsAny(s, sign) {
			return s, nil
		}
	}
}
