// SPDX-License-Identifier: MPL-2.0

package sshmachine

import (
	"context"
	"net"
	"testing"
	"time"
)

// A host that takes the TCP connection but never sends its SSH banner ends
// the dial at handshakeTimeout, and meanwhile Path() and other callers do
// not wait on the machine's lock (#521).
func TestSilentHostDoesNotBlockTheMachine(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { conn.Close() }) // says nothing
		}
	}()
	saved := handshakeTimeout
	handshakeTimeout = 700 * time.Millisecond
	t.Cleanup(func() { handshakeTimeout = saved })

	port := listener.Addr().(*net.TCPAddr).Port
	machine := New(Settings{Host: "127.0.0.1", Port: port, User: "me", Auth: "password"}, Secrets{Password: "x"}, nil)
	machine.home = "/home/me"
	started := time.Now()
	failed := make(chan error, 2)
	for i := 0; i < 2; i++ { // the second waits for the first's dial
		go func() {
			_, err := machine.files(context.Background())
			failed <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	pathDone := make(chan string, 1)
	go func() { pathDone <- machine.Path("~/projects") }()
	select {
	case got := <-pathDone:
		if got != "/home/me/projects" {
			t.Fatalf("Path: %q", got)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Path() waited for the dial")
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-failed:
			if err == nil {
				t.Fatal("a silent host connected")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the dial never ended (%s)", time.Since(started))
		}
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Fatalf("the dial took %s", took)
	}
}
