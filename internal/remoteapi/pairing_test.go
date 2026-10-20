package remoteapi

import (
	"errors"
	"fmt"
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
	pending, err := book.Request(" Galaxy S10e ")
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
	pending, _ := book.Request("Phone")
	c.at = c.at.Add(PairingTTL + time.Second)
	if _, err := book.Confirm(pending.ID, pending.Code, ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired code accepted: %v", err)
	}
}

func TestPairingTriesPerCode(t *testing.T) {
	book, _ := newBook()
	pending, _ := book.Request("Phone")
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
		pending, err := book.Request("Attacker")
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
	if _, err := book.Request("Attacker"); !errors.Is(err, ErrPairingLocked) {
		t.Fatalf("locked book still takes requests: %v", err)
	}
	offer, err := book.Offer()
	if err != nil || book.Locked {
		t.Fatalf("offer did not unlock: %v", err)
	}
	if _, err := book.Confirm(offer.ID, offer.Code, "Pixel"); err != nil {
		t.Fatalf("QR pairing after unlock: %v", err)
	}
}

func TestPairingRequestInterval(t *testing.T) {
	book, c := newBook()
	if _, err := book.Request("Phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Request("Phone"); !errors.Is(err, ErrPairingBusy) {
		t.Fatalf("second request within %s: %v", requestInterval, err)
	}
	c.at = c.at.Add(requestInterval)
	if _, err := book.Request("Phone"); err != nil {
		t.Fatal(err)
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
	pending, _ := book.Request("Phone")
	book.Cancel("other")
	if book.Pending == nil {
		t.Fatal("cancel of another pairing dropped this one")
	}
	book.Cancel(pending.ID)
	if _, err := book.Confirm(pending.ID, pending.Code, ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("cancelled pairing accepted: %v", err)
	}
}
