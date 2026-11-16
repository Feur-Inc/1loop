// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.out.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.out.String()
}

// A browser that rejects the self-signed certificate: the server's log
// explains once instead of printing Go's raw handshake error.
func TestRejectedCertificateIsNotLoggedRaw(t *testing.T) {
	var out lockedBuffer
	served := httptest.NewUnstartedServer(http.NotFoundHandler())
	served.Config.ErrorLog = newHandshakeLog(&out, time.Hour).Logger()
	served.StartTLS()
	defer served.Close()
	for range 3 {
		connection, err := tls.Dial("tcp", served.Listener.Addr().String(), &tls.Config{ServerName: "example.com"})
		if err == nil {
			connection.Close()
			t.Fatal("the test certificate was trusted")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), "refused this server's certificate") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := out.String()
	if !strings.Contains(got, "refused this server's certificate") || strings.Contains(got, "TLS handshake error") || strings.Count(got, "\n") != 1 {
		t.Fatalf("log = %q, want one explanation and no raw handshake error", got)
	}
}

func TestHandshakeLogSummarisesTLSFailures(t *testing.T) {
	var out bytes.Buffer
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	handshakes := newHandshakeLog(&out, time.Hour)
	handshakes.now = func() time.Time { return now }
	logger := handshakes.Logger()
	failure := "http: TLS handshake error from 203.0.113.9:51234: remote error: tls: unknown certificate"

	logger.Print(failure)
	if !strings.Contains(out.String(), "docs/SERVER.md") || strings.Contains(out.String(), "unknown certificate") {
		t.Fatalf("first failure = %q, want one explanation without the raw line", out.String())
	}
	out.Reset()
	for range 50 {
		now = now.Add(time.Minute)
		logger.Print(failure)
	}
	// 50 minutes: still within the hour, nothing more said.
	if out.Len() != 0 {
		t.Fatalf("failures within the interval wrote %q", out.String())
	}
	now = now.Add(15 * time.Minute)
	logger.Print(failure)
	if got := out.String(); !strings.Contains(got, "51 more failed TLS handshakes") || strings.Count(got, "\n") != 1 {
		t.Fatalf("summary = %q, want one line counting 51", got)
	}
	out.Reset()
	logger.Print("http: panic serving 10.0.0.2:4000: boom")
	if !strings.Contains(out.String(), "panic serving") {
		t.Fatalf("other errors must pass through, got %q", out.String())
	}
}

func TestPrivateAddress(t *testing.T) {
	interfaces := func(cidrs ...string) func() ([]net.Addr, error) {
		return func() ([]net.Addr, error) {
			var addresses []net.Addr
			for _, cidr := range cidrs {
				ip, network, err := net.ParseCIDR(cidr)
				if err != nil {
					t.Fatal(err)
				}
				network.IP = ip
				addresses = append(addresses, network)
			}
			return addresses, nil
		}
	}
	container := interfaces("127.0.0.1/8", "::1/128", "172.18.0.5/16", "fe80::42:acff:fe12:5/64")
	host := interfaces("127.0.0.1/8", "10.0.0.4/24", "198.51.100.7/24")
	for _, test := range []struct {
		ip      string
		addrs   func() ([]net.Addr, error)
		private bool
	}{
		{"127.0.0.1", host, true},
		{"::1", host, true},
		{"10.0.0.4", host, true},
		{"192.168.1.20", host, true},
		{"fd00::5", host, true},
		{"198.51.100.7", host, false},
		{"2001:db8::1", host, false},
		{"0.0.0.0", container, true},
		{"::", container, true},
		{"0.0.0.0", host, false},
	} {
		err := privateAddress(net.ParseIP(test.ip), test.addrs)
		if (err == nil) != test.private {
			t.Errorf("privateAddress(%s) = %v, want private %v", test.ip, err, test.private)
		}
	}
}

func TestPlainListenerServesLoopbackOnly(t *testing.T) {
	var warning bytes.Buffer
	listener, err := plainListener("127.0.0.1:0", false, &warning)
	if err != nil {
		t.Fatal(err)
	}
	if warning.Len() != 0 {
		t.Fatalf("loopback warned: %q", warning.String())
	}
	served := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})}
	go served.Serve(listener)
	defer served.Close()
	response, err := http.Get("http://" + listener.Addr().String() + "/api/info")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("plain HTTP answered %q", body)
	}
}

func TestSetupMessageNamesTheProxy(t *testing.T) {
	direct := setupMessage(47101, "secret", false)
	if !strings.Contains(direct, "https://<server>:47101/app/#setup=secret") || strings.Contains(direct, "<proxy>") {
		t.Fatalf("direct setup message = %q", direct)
	}
	proxied := setupMessage(47101, "secret", true)
	if !strings.Contains(proxied, "https://<proxy>/app/#setup=secret") {
		t.Fatalf("proxied setup message = %q", proxied)
	}
}
