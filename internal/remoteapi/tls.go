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
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Certificate loads the TLS certificate phones pin by its fingerprint: the
// one given (certPath and keyPath), or server.crt and server.key in dir,
// generated on first use.
func Certificate(dir, certPath, keyPath string) (tls.Certificate, error) {
	if (certPath == "") != (keyPath == "") {
		return tls.Certificate{}, fmt.Errorf("Set both -tls-cert and -tls-key")
	}
	if certPath == "" {
		certPath, keyPath = filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
		_, certErr := os.Stat(certPath)
		_, keyErr := os.Stat(keyPath)
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
			if err = os.MkdirAll(dir, 0700); err != nil {
				return tls.Certificate{}, err
			}
			if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
				return tls.Certificate{}, err
			}
			if err = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0600); err != nil {
				return tls.Certificate{}, err
			}
		}
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}

// Fingerprint is the SHA-256 of the certificate, in lowercase hexadecimal.
func Fingerprint(certificate tls.Certificate) string {
	sum := sha256.Sum256(certificate.Certificate[0])
	return hex.EncodeToString(sum[:])
}
