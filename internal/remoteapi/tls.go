// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"1loop/internal/core"
)

// Certificate loads the TLS certificate phones pin by its fingerprint: the
// one given (certPath and keyPath), or server.crt and server.key in dir,
// generated on first use. It is never renewed, since a new one would have a
// new fingerprint: the clients that pin it ignore its dates (#517).
func Certificate(dir, certPath, keyPath string) (tls.Certificate, error) {
	if (certPath == "") != (keyPath == "") {
		return tls.Certificate{}, fmt.Errorf("Set both -tls-cert and -tls-key")
	}
	if certPath == "" {
		certPath, keyPath = filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
		_, certErr := os.Stat(certPath)
		_, keyErr := os.Stat(keyPath)
		switch {
		case os.IsNotExist(certErr) && !os.IsNotExist(keyErr):
			return tls.Certificate{}, fmt.Errorf("%s is missing next to %s: restore it, or delete %s too to make a new certificate (paired devices then pair again)", certPath, keyPath, keyPath)
		case os.IsNotExist(keyErr) && !os.IsNotExist(certErr):
			return tls.Certificate{}, fmt.Errorf("%s is missing next to %s: restore it, or delete %s too to make a new certificate (paired devices then pair again)", keyPath, certPath, certPath)
		}
		if os.IsNotExist(certErr) && os.IsNotExist(keyErr) {
			key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return tls.Certificate{}, err
			}
			serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
			if err != nil {
				return tls.Certificate{}, err
			}
			template := &x509.Certificate{
				SerialNumber: serial,
				Subject:      pkix.Name{CommonName: "1loop server"},
				NotBefore:    time.Now().Add(-time.Minute),
				NotAfter:     time.Now().AddDate(1, 0, 0),
				KeyUsage:     x509.KeyUsageDigitalSignature,
				ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				DNSNames:     []string{"localhost"},
			}
			raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
			if err != nil {
				return tls.Certificate{}, err
			}
			private, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				return tls.Certificate{}, err
			}
			// Each one whole or not at all, the certificate last (#523): a
			// crash never leaves a certificate without its key.
			if err = core.WriteFileSafely(keyPath, 0600, false, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})); err != nil {
				return tls.Certificate{}, err
			}
			if err = core.WriteFileSafely(certPath, 0600, false, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})); err != nil {
				return tls.Certificate{}, err
			}
		}
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		var unreadable *fs.PathError
		if err != nil && !errors.As(err, &unreadable) {
			// Damaged, or the halves of two certificates: never replaced
			// here, since a new one would unpair every device.
			return tls.Certificate{}, fmt.Errorf("%s and %s do not make a certificate (%v): restore both from a backup, or delete both to make a new one (paired devices then pair again)", certPath, keyPath, err)
		}
		return certificate, err
	}
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("-tls-cert %s and -tls-key %s: %v", certPath, keyPath, err)
	}
	return certificate, nil
}

// Fingerprint is the SHA-256 of the certificate, in lowercase hexadecimal.
func Fingerprint(certificate tls.Certificate) string {
	sum := sha256.Sum256(certificate.Certificate[0])
	return hex.EncodeToString(sum[:])
}
