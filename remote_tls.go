package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A self-hosted server can be trusted by the fingerprint read from its
// console. No credentials are sent through an unverified TLS connection.
func secureRemoteClient(address, fingerprint string) (*http.Client, string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(address), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, "", fmt.Errorf("Enter an HTTPS server address, such as https://192.168.1.20:47101")
	}
	settings := &tls.Config{MinVersion: tls.VersionTLS12}
	if fingerprint != "" {
		expected, err := hex.DecodeString(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fingerprint), ":", "")))
		if err != nil || len(expected) != sha256.Size {
			return nil, "", fmt.Errorf("Enter the complete SHA-256 certificate fingerprint from the server console")
		}
		// Exact certificate pinning replaces CA/hostname validation for this
		// user-approved private server; it never falls back to accepting any cert.
		settings.InsecureSkipVerify = true
		settings.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return fmt.Errorf("Server sent no certificate")
			}
			certificate := state.PeerCertificates[0]
			actual := sha256.Sum256(certificate.Raw)
			if subtle.ConstantTimeCompare(actual[:], expected) != 1 {
				return fmt.Errorf("Server certificate fingerprint changed; verify it on the server console")
			}
			now := time.Now()
			if now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
				return fmt.Errorf("Server certificate has expired or is not valid yet")
			}
			return nil
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = settings
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, parsed.String(), nil
}
