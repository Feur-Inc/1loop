// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"testing"
	"time"
)

// A pinned certificate past its dates still connects: the server never
// renews it (#517). Another certificate is refused.
func TestPinnedCertificateIgnoresDates(t *testing.T) {
	certificate := func() *x509.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().AddDate(-2, 0, 0), NotAfter: time.Now().AddDate(-1, 0, 0)}
		raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		parsed, _ := x509.ParseCertificate(raw)
		return parsed
	}
	expired, other := certificate(), certificate()
	sum := sha256.Sum256(expired.Raw)
	verify := pinnedCertificate(sum[:])
	if err := verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{expired}}); err != nil {
		t.Fatalf("expired pinned certificate: %v", err)
	}
	if err := verify(tls.ConnectionState{PeerCertificates: []*x509.Certificate{other}}); err == nil {
		t.Fatal("another certificate was accepted")
	}
	if err := verify(tls.ConnectionState{}); err == nil {
		t.Fatal("no certificate was accepted")
	}
}
