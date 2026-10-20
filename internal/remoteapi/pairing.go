package remoteapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"1loop/internal/core"
)

// How a phone pairs with the desktop: it asks and types the six digits the
// computer shows (Request), or it scans the QR code the user opened on the
// computer, which carries a 128-bit secret (Offer). Every pairing expires
// after PairingTTL and allows TriesPerPairing wrong codes. A new request
// does not reset the count of wrong codes: after MaxFailedCodes of them,
// requests and typed codes stop until the user starts a pairing on the
// computer, so nobody on the network can try codes until one works.
const (
	PairingTTL      = 2 * time.Minute
	TriesPerPairing = 5
	MaxFailedCodes  = 20
	requestInterval = 3 * time.Second // a phone's requests do not pile up sheets
)

var (
	ErrPairingLocked  = errors.New("Too many wrong codes. Start pairing from Settings → Phone remote on the computer.")
	ErrPairingExpired = errors.New("This code has expired. Ask for a new one.")
	ErrPairingBusy    = errors.New("A pairing was just requested. Try again in a few seconds.")
)

// Pairing is the one pairing waiting for its code.
type Pairing struct {
	ID      string
	Device  string
	Code    string // six digits, or the QR secret
	QR      bool
	tries   int
	expires time.Time
}

// PairingBook is not safe for concurrent use: the desktop keeps it on its
// UI thread.
type PairingBook struct {
	Pending     *Pairing
	Locked      bool
	failures    int
	lastRequest time.Time
	Now         func() time.Time
}

func (book *PairingBook) now() time.Time {
	if book.Now != nil {
		return book.Now()
	}
	return time.Now()
}

// Request is a phone asking to pair; the computer shows the returned code.
func (book *PairingBook) Request(device string) (*Pairing, error) {
	if book.Locked {
		return nil, ErrPairingLocked
	}
	now := book.now()
	if now.Sub(book.lastRequest) < requestInterval {
		return nil, ErrPairingBusy
	}
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return nil, err
	}
	book.lastRequest = now
	book.Pending = &Pairing{ID: core.Identifier()[:16], Device: DeviceName(device), Code: fmt.Sprintf("%06d", number.Int64()), expires: now.Add(PairingTTL)}
	return book.Pending, nil
}

// Offer is the user opening the QR code on the computer. It lifts a lock:
// the user is at the computer.
func (book *PairingBook) Offer() (*Pairing, error) {
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	book.Locked, book.failures = false, 0
	book.Pending = &Pairing{ID: core.Identifier()[:16], Device: "Phone", Code: hex.EncodeToString(secret), QR: true, expires: book.now().Add(PairingTTL)}
	return book.Pending, nil
}

// Cancel drops the pending pairing (its sheet was closed).
func (book *PairingBook) Cancel(id string) {
	if book.Pending != nil && book.Pending.ID == id {
		book.Pending = nil
	}
}

// Confirm checks a code. On success the pairing is used up and returned,
// with the device name the phone gave (a QR pairing takes it from device).
func (book *PairingBook) Confirm(id, code, device string) (*Pairing, error) {
	if book.Locked {
		return nil, ErrPairingLocked
	}
	pairing := book.Pending
	if pairing == nil || pairing.ID != id || book.now().After(pairing.expires) {
		return nil, ErrPairingExpired
	}
	if subtle.ConstantTimeCompare([]byte(pairing.Code), []byte(strings.TrimSpace(code))) != 1 {
		pairing.tries++
		book.failures++
		switch {
		case book.failures >= MaxFailedCodes:
			book.Locked, book.Pending = true, nil
			return nil, ErrPairingLocked
		case pairing.tries >= TriesPerPairing:
			book.Pending = nil
			return nil, errors.New("Too many wrong codes. Ask for a new one.")
		}
		return nil, errors.New("Wrong code.")
	}
	book.Pending = nil
	if pairing.QR && strings.TrimSpace(device) != "" {
		pairing.Device = DeviceName(device)
	}
	return pairing, nil
}

// DeviceName is the name a phone gives, trimmed to fit the settings.
func DeviceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Phone"
	}
	if runes := []rune(name); len(runes) > 40 {
		return string(runes[:40])
	}
	return name
}
