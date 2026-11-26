// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"1loop/internal/core"
)

// How a device pairs, with the desktop or with a headless server: it asks
// (Request) and the code is typed on the other side, or it scans the QR code
// the user opened on the computer, which carries a 128-bit secret (Offer).
// The desktop shows the six digits and the phone types them; a server gives
// them to the device that asked and a paired device, or its console, types
// them to approve it (Approval). Both follow the same policy: every pairing
// expires after PairingTTL and allows TriesPerPairing wrong codes; a client
// asks at most once per requestInterval; device names are cleaned the same
// way (DeviceName). A new request does not reset the count of wrong codes:
// after MaxFailedCodes of them, requests and typed codes stop until the
// user unlocks pairing where they are trusted (Offer on the computer,
// Unlock from a server's console or its phone app), so nobody on the
// network can try codes until one works.
const (
	PairingTTL      = 2 * time.Minute
	TriesPerPairing = 5
	MaxFailedCodes  = 20
	requestInterval = 3 * time.Second // a client's requests do not pile up sheets
)

var (
	ErrPairingLocked      = errors.New("Too many wrong codes. Start pairing from Settings → Phone on the computer.")
	ErrPairingExpired     = errors.New("This code has expired. Ask for a new one.")
	ErrPairingBusy        = errors.New("A pairing was just requested. Try again in a few seconds.")
	ErrPairingWaiting     = errors.New("Another pairing is waiting for its code on the computer. Try again once it is done, or in two minutes.")
	ErrPairingsFull       = errors.New("Too many pending pairings; try again later")
	ErrPairingNotApproved = errors.New("Waiting for approval on a paired device or in the server console")
	ErrWrongCode          = errors.New("Wrong code.")
	ErrTooManyTries       = errors.New("Too many wrong codes. Ask for a new one.")
)

// Pairing is a pairing waiting for its code. The book hands out copies.
type Pairing struct {
	ID       string
	Device   string
	Code     string // six digits, or the QR secret
	QR       bool
	Client   string // the address that asked: PerClient pairings each
	Approved bool
	Expires  time.Time
	tries    int
}

// PairingBook is the pairings waiting for their code, safe for concurrent
// use. Its zero value is the desktop's: one pairing at a time, shown on
// screen, the code itself being the approval.
type PairingBook struct {
	// Slots is how many pairings may wait at once (1 when 0): the desktop
	// shows one sheet, a server lists them on its paired devices.
	Slots int
	// PerClient is how many of them one client address may hold (1 when
	// 0); past it, the client's new request replaces its own oldest one,
	// so one device on the network cannot hold every slot (#532).
	PerClient int
	// Approval: a pairing must also be approved (Approve) before Confirm
	// gives it a token, as on a headless server.
	Approval bool
	Now      func() time.Time

	mu          sync.Mutex
	pending     []*Pairing
	locked      bool
	failures    int
	lastRequest map[string]time.Time
}

func (book *PairingBook) now() time.Time {
	if book.Now != nil {
		return book.Now()
	}
	return time.Now()
}

// pruneLocked drops the expired pairings and the old request times.
func (book *PairingBook) pruneLocked(now time.Time) {
	book.pending = slices.DeleteFunc(book.pending, func(pairing *Pairing) bool { return !now.Before(pairing.Expires) })
	for client, at := range book.lastRequest {
		if now.Sub(at) >= requestInterval {
			delete(book.lastRequest, client)
		}
	}
}

func (book *PairingBook) findLocked(id string) int {
	return slices.IndexFunc(book.pending, func(pairing *Pairing) bool { return id != "" && pairing.ID == id })
}

func (book *PairingBook) removeLocked(id string) {
	book.pending = slices.DeleteFunc(book.pending, func(pairing *Pairing) bool { return pairing.ID == id })
}

// Request is a device asking to pair from client, its address; the
// computer shows the returned code, or a server gives it to the device.
func (book *PairingBook) Request(device, client string) (Pairing, error) {
	book.mu.Lock()
	defer book.mu.Unlock()
	if book.locked {
		return Pairing{}, ErrPairingLocked
	}
	now := book.now()
	book.pruneLocked(now)
	if _, recent := book.lastRequest[client]; recent {
		return Pairing{}, ErrPairingBusy
	}
	// A pairing waiting for its code stays until it is answered, cancelled
	// or expires: anything on the network could otherwise keep replacing
	// it, and the real phone's code would never be the one shown (#532).
	// Only the client's own pairings make way for its new one.
	var mine []*Pairing
	for _, pairing := range book.pending {
		if pairing.Client == client && !pairing.Approved && !pairing.QR {
			mine = append(mine, pairing)
		}
	}
	if len(mine) >= max(book.PerClient, 1) {
		book.removeLocked(slices.MinFunc(mine, func(a, b *Pairing) int { return a.Expires.Compare(b.Expires) }).ID)
	}
	if slots := max(book.Slots, 1); len(book.pending) >= slots {
		if slots == 1 {
			return Pairing{}, ErrPairingWaiting
		}
		return Pairing{}, ErrPairingsFull
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return Pairing{}, err
	}
	if book.lastRequest == nil {
		book.lastRequest = map[string]time.Time{}
	}
	book.lastRequest[client] = now
	pairing := &Pairing{ID: core.Identifier()[:16], Device: DeviceName(device), Code: fmt.Sprintf("%06d", number.Int64()), Client: client, Expires: now.Add(PairingTTL)}
	book.pending = append(book.pending, pairing)
	return *pairing, nil
}

// Offer is the user opening the QR code on the computer. It replaces the
// pairings waiting and lifts a lock: the user is at the computer.
func (book *PairingBook) Offer() (Pairing, error) {
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return Pairing{}, err
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	book.locked, book.failures = false, 0
	pairing := &Pairing{ID: core.Identifier()[:16], Device: "Phone", Code: hex.EncodeToString(secret), QR: true, Approved: true, Expires: book.now().Add(PairingTTL)}
	book.pending = []*Pairing{pairing}
	return *pairing, nil
}

// Cancel drops a pending pairing (its sheet was closed, or it was refused).
func (book *PairingBook) Cancel(id string) {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.removeLocked(id)
}

// Clear drops every pending pairing and the clients' last requests (a
// server stopped sharing).
func (book *PairingBook) Clear() {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.pending, book.lastRequest = nil, nil
}

// Locked tells whether too many wrong codes stopped pairing.
func (book *PairingBook) Locked() bool {
	book.mu.Lock()
	defer book.mu.Unlock()
	return book.locked
}

// Unlock lifts the lock from where the user is trusted (a server's console
// or its phone app), as Offer does on the computer.
func (book *PairingBook) Unlock() {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.locked, book.failures = false, 0
}

// Waiting are the pairings still waiting for their code or approval.
func (book *PairingBook) Waiting() []Pairing {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.pruneLocked(book.now())
	waiting := []Pairing{}
	for _, pairing := range book.pending {
		if !pairing.Approved {
			waiting = append(waiting, *pairing)
		}
	}
	return waiting
}

// Pending is how many pairings are pending, approved or not.
func (book *PairingBook) Pending() int {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.pruneLocked(book.now())
	return len(book.pending)
}

// Get is the pending pairing id, if it is still there.
func (book *PairingBook) Get(id string) (Pairing, bool) {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.pruneLocked(book.now())
	if index := book.findLocked(id); index >= 0 {
		return *book.pending[index], true
	}
	return Pairing{}, false
}

// checkLocked compares a code with the pairing's; a wrong one uses up one
// of its tries and, when counted, one of the book's before it locks.
func (book *PairingBook) checkLocked(pairing *Pairing, code string, counted bool) error {
	if subtle.ConstantTimeCompare([]byte(pairing.Code), []byte(strings.TrimSpace(code))) == 1 {
		return nil
	}
	pairing.tries++
	if counted {
		book.failures++
		if book.failures >= MaxFailedCodes {
			book.locked, book.pending = true, nil
			return ErrPairingLocked
		}
	}
	if pairing.tries >= TriesPerPairing {
		book.removeLocked(pairing.ID)
		return ErrTooManyTries
	}
	return ErrWrongCode
}

// Approve is a paired device, or a server's console, typing the code the
// device that asked shows. Those are trusted: their wrong codes use up the
// pairing's tries but do not lock the book.
func (book *PairingBook) Approve(id, code string) error {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.pruneLocked(book.now())
	index := book.findLocked(id)
	if index < 0 {
		return ErrPairingExpired
	}
	pairing := book.pending[index]
	if err := book.checkLocked(pairing, code, false); err != nil {
		return err
	}
	pairing.Approved = true
	return nil
}

// Confirm checks a code. On success the pairing is used up and returned,
// with the device name the phone gave (a QR pairing takes it from device).
// With Approval, the right code of a pairing not approved yet returns
// ErrPairingNotApproved and the pairing keeps waiting.
func (book *PairingBook) Confirm(id, code, device string) (Pairing, error) {
	book.mu.Lock()
	defer book.mu.Unlock()
	if book.locked {
		return Pairing{}, ErrPairingLocked
	}
	book.pruneLocked(book.now())
	index := book.findLocked(id)
	if index < 0 {
		return Pairing{}, ErrPairingExpired
	}
	pairing := book.pending[index]
	if err := book.checkLocked(pairing, code, true); err != nil {
		return Pairing{}, err
	}
	if book.Approval && !pairing.Approved {
		return Pairing{}, ErrPairingNotApproved
	}
	book.removeLocked(id)
	if pairing.QR && strings.TrimSpace(device) != "" {
		pairing.Device = DeviceName(device)
	}
	return *pairing, nil
}

// Restore puts back a pairing that Confirm used up when its device could
// not be saved, so that the device can try again.
func (book *PairingBook) Restore(pairing Pairing) {
	book.mu.Lock()
	defer book.mu.Unlock()
	if book.findLocked(pairing.ID) < 0 {
		book.pending = append(book.pending, &pairing)
	}
}

// ClientAddress is the IP address a request comes from.
func ClientAddress(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

// MaxDeviceName is how many characters of a device's name are kept.
const MaxDeviceName = 40

// DeviceName is the name a device gives, cleaned to show in a settings
// list or a console: control and format characters (line breaks, terminal
// escapes, bidirectional overrides) and invalid UTF-8 are dropped, runs of
// spaces become one, and it is cut to MaxDeviceName characters.
func DeviceName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "Phone"
	}
	if runes := []rune(name); len(runes) > MaxDeviceName {
		return strings.TrimSpace(string(runes[:MaxDeviceName]))
	}
	return name
}
