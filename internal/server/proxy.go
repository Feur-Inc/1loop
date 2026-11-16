// SPDX-License-Identifier: MPL-2.0

package server

// Behind a reverse proxy (#332): browsers cannot pin the generated
// certificate's fingerprint, so they warn on every visit and the console
// fills with their failed handshakes. A proxy with a real certificate
// (Caddy, Traefik) fixes the warning; the optional plain-HTTP listener
// spares it from trusting the self-signed one, and the handshake log keeps
// the remaining failures from burying the pairing messages.

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// plainListener listens for a reverse proxy on address over plain HTTP.
// Without TLS, tokens and conversations cross the network in the clear: it
// refuses an address the proxy is not alone in reaching unless allowPublic
// says so, and then warns.
func plainListener(address string, allowPublic bool, warn io.Writer) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	if err = privateAddress(listener.Addr().(*net.TCPAddr).IP, net.InterfaceAddrs); err != nil {
		if !allowPublic {
			listener.Close()
			return nil, fmt.Errorf("-http-listen %s: %v. Plain HTTP is for a reverse proxy on this machine or a private network: listen on 127.0.0.1, a private address or inside a container, or add -http-allow-public if a firewall keeps everyone else out", address, err)
		}
		fmt.Fprintf(warn, "\nWARNING: plain HTTP on %s (%v). Paired devices' tokens and conversations cross the network unencrypted to anyone who reaches it: keep it behind a firewall that lets only the reverse proxy in.\n\n", listener.Addr(), err)
	}
	return listener, nil
}

// privateAddress says why ip is reachable beyond this machine and private
// networks, or nil. An unspecified address (0.0.0.0, ::) listens on every
// interface: it is private when every interface's address is, as in a
// container on a Docker network.
func privateAddress(ip net.IP, interfaces func() ([]net.Addr, error)) error {
	if !ip.IsUnspecified() {
		if ip.IsLoopback() || ip.IsPrivate() {
			return nil
		}
		return fmt.Errorf("%s is a public address", ip)
	}
	addresses, err := interfaces()
	if err != nil {
		return fmt.Errorf("cannot list this machine's addresses: %v", err)
	}
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if !ok {
			continue
		}
		local := network.IP
		// Link-local addresses reach only the same link, such as a
		// Docker bridge: every IPv6 interface has one.
		if local.IsLoopback() || local.IsPrivate() || local.IsLinkLocalUnicast() {
			continue
		}
		return fmt.Errorf("listening on every interface includes the public address %s", local)
	}
	return nil
}

// handshakeLog is the HTTPS server's error log: it passes every line on to
// out but the failed TLS handshakes, which a browser that does not trust the
// certificate, or a port scanner, causes on every visit. The first is told
// once with what to do; the next are counted and summed up at most every
// interval.
type handshakeLog struct {
	out      io.Writer
	interval time.Duration
	now      func() time.Time

	mu        sync.Mutex
	told      bool
	count     int
	lastShown time.Time
}

func newHandshakeLog(out io.Writer, interval time.Duration) *handshakeLog {
	return &handshakeLog{out: out, interval: interval, now: time.Now}
}

// Logger is a log.Logger for http.Server's ErrorLog that writes through it.
func (handshakes *handshakeLog) Logger() *log.Logger {
	return log.New(handshakes, "", log.LstdFlags)
}

func (handshakes *handshakeLog) Write(line []byte) (int, error) {
	if !bytes.Contains(line, []byte("http: TLS handshake error")) {
		return handshakes.out.Write(line)
	}
	handshakes.mu.Lock()
	defer handshakes.mu.Unlock()
	now := handshakes.now()
	if !handshakes.told {
		handshakes.told = true
		handshakes.lastShown = now
		_, err := fmt.Fprintf(handshakes.out, "%s A client refused this server's certificate or did not finish its TLS handshake: browsers do so with the generated one. Further failures are summed up here; to stop them, see \"Behind a reverse proxy\" in docs/SERVER.md.\n", now.Format("2006/01/02 15:04:05"))
		return len(line), err
	}
	handshakes.count++
	if now.Sub(handshakes.lastShown) < handshakes.interval {
		return len(line), nil
	}
	count := handshakes.count
	handshakes.count = 0
	handshakes.lastShown = now
	plural := "s"
	if count == 1 {
		plural = ""
	}
	_, err := fmt.Fprintf(handshakes.out, "%s %d more failed TLS handshake%s since the last report.\n", now.Format("2006/01/02 15:04:05"), count, plural)
	return len(line), err
}
