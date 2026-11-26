// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock lets a test move time forward.
type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func newBook() (*PairingBook, *clock) {
	c := &clock{at: time.Unix(1_000_000, 0)}
	return &PairingBook{Now: c.now}, c
}

func wrong(code string) string {
	if code == "000000" {
		return "000001"
	}
	return "000000"
}

func TestPairingRightCode(t *testing.T) {
	book, _ := newBook()
	pending, err := book.Request(" Galaxy S10e ", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Code) != 6 {
		t.Fatalf("code %q is not six digits", pending.Code)
	}
	paired, err := book.Confirm(pending.ID, pending.Code, "ignored for a typed code")
	if err != nil || paired.Device != "Galaxy S10e" {
		t.Fatalf("confirm: %v %+v", err, paired)
	}
	if _, err := book.Confirm(pending.ID, pending.Code, ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("a code works only once, got %v", err)
	}
}

func TestPairingExpires(t *testing.T) {
	book, c := newBook()
	pending, _ := book.Request("Phone", "192.0.2.1")
	c.at = c.at.Add(PairingTTL + time.Second)
	if _, err := book.Confirm(pending.ID, pending.Code, ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired code accepted: %v", err)
	}
}

func TestPairingTriesPerCode(t *testing.T) {
	book, _ := newBook()
	pending, _ := book.Request("Phone", "192.0.2.1")
	for i := 0; i < TriesPerPairing; i++ {
		if _, err := book.Confirm(pending.ID, wrong(pending.Code), ""); err == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if _, err := book.Confirm(pending.ID, pending.Code, ""); err == nil {
		t.Fatal("the right code still works after too many wrong ones")
	}
}

// A new request must not give a guesser a fresh budget: after
// MaxFailedCodes wrong codes in total, pairing stops until the user offers
// a QR code on the computer.
func TestPairingGlobalLock(t *testing.T) {
	book, c := newBook()
	failures := 0
	for failures < MaxFailedCodes {
		c.at = c.at.Add(requestInterval)
		pending, err := book.Request("Attacker", "192.0.2.1")
		if err != nil {
			t.Fatalf("request %d: %v", failures, err)
		}
		for i := 0; i < TriesPerPairing && failures < MaxFailedCodes; i++ {
			_, err = book.Confirm(pending.ID, wrong(pending.Code), "")
			failures++
		}
		if failures == MaxFailedCodes && !errors.Is(err, ErrPairingLocked) {
			t.Fatalf("not locked after %d wrong codes: %v", failures, err)
		}
	}
	c.at = c.at.Add(time.Hour)
	if _, err := book.Request("Attacker", "192.0.2.1"); !errors.Is(err, ErrPairingLocked) {
		t.Fatalf("locked book still takes requests: %v", err)
	}
	offer, err := book.Offer()
	if err != nil || book.Locked() {
		t.Fatalf("offer did not unlock: %v", err)
	}
	if _, err := book.Confirm(offer.ID, offer.Code, "Pixel"); err != nil {
		t.Fatalf("QR pairing after unlock: %v", err)
	}
}

func TestPairingRequestInterval(t *testing.T) {
	book, c := newBook()
	if _, err := book.Request("Phone", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Request("Phone", "192.0.2.1"); !errors.Is(err, ErrPairingBusy) {
		t.Fatalf("second request within %s: %v", requestInterval, err)
	}
	// Another client does not replace the pairing on screen (#532)...
	c.at = c.at.Add(requestInterval)
	pending := book.Waiting()[0]
	if _, err := book.Request("Laptop", "192.0.2.2"); !errors.Is(err, ErrPairingWaiting) {
		t.Fatalf("a request replaced the pending pairing: %v", err)
	}
	if waiting := book.Waiting(); len(waiting) != 1 || waiting[0].ID != pending.ID {
		t.Fatalf("pending: %+v", waiting)
	}
	// ...until it is cancelled, or expires.
	book.Cancel(pending.ID)
	if _, err := book.Request("Laptop", "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	c.at = c.at.Add(PairingTTL + time.Second)
	if _, err := book.Request("Phone", "192.0.2.1"); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
}

func TestPairingQR(t *testing.T) {
	book, _ := newBook()
	offer, err := book.Offer()
	if err != nil {
		t.Fatal(err)
	}
	if len(offer.Code) != 32 || !offer.QR {
		t.Fatalf("QR secret %q is not 128 bits", offer.Code)
	}
	if _, err := book.Confirm(offer.ID, offer.Code[:6], ""); err == nil {
		t.Fatal("part of the secret accepted")
	}
	paired, err := book.Confirm(offer.ID, offer.Code, fmt.Sprintf("%050d", 7))
	if err != nil || len([]rune(paired.Device)) != 40 {
		t.Fatalf("QR confirm: %v %+v", err, paired)
	}
}

func TestPairingCancel(t *testing.T) {
	book, _ := newBook()
	pending, _ := book.Request("Phone", "192.0.2.1")
	book.Cancel("other")
	if _, ok := book.Get(pending.ID); !ok {
		t.Fatal("cancel of another pairing dropped this one")
	}
	book.Cancel(pending.ID)
	if _, err := book.Confirm(pending.ID, pending.Code, ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("cancelled pairing accepted: %v", err)
	}
}

// A server's pairings wait for approval: the right code alone is not enough,
// and an approver's wrong codes use up the pairing's tries but never lock
// the book.
func TestPairingApproval(t *testing.T) {
	book, _ := newBook()
	book.Approval, book.Slots, book.PerClient = true, 8, 2
	pending, err := book.Request("Laptop", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.Confirm(pending.ID, pending.Code, ""); !errors.Is(err, ErrPairingNotApproved) {
		t.Fatalf("confirmed before approval: %v", err)
	}
	if err := book.Approve(pending.ID, wrong(pending.Code)); !errors.Is(err, ErrWrongCode) {
		t.Fatalf("approved with a wrong code: %v", err)
	}
	if err := book.Approve(pending.ID, pending.Code); err != nil {
		t.Fatal(err)
	}
	if len(book.Waiting()) != 0 {
		t.Fatal("an approved pairing is still listed as waiting")
	}
	if paired, err := book.Confirm(pending.ID, pending.Code, ""); err != nil || paired.Device != "Laptop" {
		t.Fatalf("confirm after approval: %v %+v", err, paired)
	}
	for i := 0; i < MaxFailedCodes; i++ {
		other, err := book.Request("Other", fmt.Sprint("192.0.2.", 10+i))
		if err != nil {
			t.Fatal(err)
		}
		for try := 0; try < TriesPerPairing; try++ {
			book.Approve(other.ID, wrong(other.Code))
		}
	}
	if book.Locked() {
		t.Fatal("an approver's wrong codes locked the book")
	}
}

// One client cannot hold every slot: past PerClient, its new request
// replaces its own oldest one, and the other clients' stay (#532).
func TestPairingSlotsPerClient(t *testing.T) {
	book, c := newBook()
	book.Slots, book.PerClient = 4, 2
	phone, _ := book.Request("Phone", "192.0.2.8")
	for i := 0; i < 10; i++ {
		c.at = c.at.Add(requestInterval)
		if _, err := book.Request("Flood", "192.0.2.7"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	mine := 0
	for _, pairing := range book.Waiting() {
		if pairing.Client == "192.0.2.7" {
			mine++
		}
	}
	if _, ok := book.Get(phone.ID); !ok || mine != 2 {
		t.Fatalf("phone kept %v, flooder holds %d", ok, mine)
	}
	if _, err := book.Request("Tablet", "192.0.2.9"); err != nil {
		t.Fatalf("another client after the flood: %v", err)
	}
	if _, err := book.Request("Desk", "192.0.2.10"); !errors.Is(err, ErrPairingsFull) {
		t.Fatalf("past the slots: %v", err)
	}
	book.Unlock()
	book.Clear()
	if len(book.Waiting()) != 0 {
		t.Fatal("clear left pairings")
	}
}

func TestDeviceName(t *testing.T) {
	for in, want := range map[string]string{
		"  Pixel   8\tPro ":               "Pixel 8 Pro",
		"":                                "Phone",
		"\x1b[31m\n":                      "[31m",
		"Evil‮gnp.exe":                    "Evilgnp.exe",
		"bad\xffutf8":                     "badutf8",
		strings.Repeat("é", 60):           strings.Repeat("é", MaxDeviceName),
		strings.Repeat("a", 39) + " tail": strings.Repeat("a", 39),
		"Line one\r\nLine two":            "Line one Line two",
	} {
		if got := DeviceName(in); got != want {
			t.Errorf("DeviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The book is used from HTTP handlers on many goroutines (go test -race).
func TestPairingConcurrent(t *testing.T) {
	book := &PairingBook{Slots: 32, PerClient: 2, Approval: true}
	var wait sync.WaitGroup
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			pending, err := book.Request("Phone", fmt.Sprint("192.0.2.", i))
			if err != nil {
				return
			}
			book.Approve(pending.ID, pending.Code)
			book.Waiting()
			book.Confirm(pending.ID, pending.Code, "")
		}()
	}
	wait.Wait()
}
