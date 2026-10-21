package ui

// The desktop's client for a headless 1loop server (see docs/SERVER.md): requests
// carry the paired token, and the server's certificate is checked against
// the fingerprint its console printed.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"1loop/internal/agent"
)

type serverAddress struct {
	URL         string `json:"url"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Token       string `json:"token,omitempty"`
}

type remoteClient struct {
	http    *http.Client
	address serverAddress
}

func newRemoteClient(address serverAddress) (*remoteClient, error) {
	client, base, err := secureRemoteClient(address.URL, address.Fingerprint)
	if err != nil {
		return nil, err
	}
	address.URL = base
	return &remoteClient{client, address}, nil
}

func (client *remoteClient) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	kind := "application/json"
	if raw, ok := body.(io.Reader); ok { // a file sent as it is
		reader, kind = raw, "application/octet-stream"
	} else if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.address.URL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", kind)
	if client.address.Token != "" {
		req.Header.Set("Authorization", "Bearer "+client.address.Token)
	}
	response, err := client.http.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		var problem struct {
			Error   string
			Handoff *agent.HandoffPlan
		}
		json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&problem)
		if problem.Error == "" {
			problem.Error = response.Status
		}
		if problem.Handoff != nil {
			return nil, handoffRequired{problem.Error, *problem.Handoff}
		}
		return nil, fmt.Errorf("%s", problem.Error)
	}
	return response, nil
}

func (client *remoteClient) json(ctx context.Context, method, path string, body, result any) error {
	response, err := client.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if result == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(result)
}

// secureRemoteClient trusts a self-hosted server by the fingerprint read
// from its console. No credentials go through an unverified TLS connection.
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

// handoffRequired is the server asking how the chat's new model receives
// the conversation, before it sends the message.
type handoffRequired struct {
	text string
	plan agent.HandoffPlan
}

func (required handoffRequired) Error() string { return required.text }
