// SPDX-License-Identifier: MPL-2.0

package sshmachine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// A line of ~/.ssh/known_hosts with a key type Go's ssh package does not
// know is skipped; the other lines still count.
func TestKnownHostsWithUnknownKeyType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	KnownHostsFile = filepath.Join(t.TempDir(), "known_hosts")
	t.Cleanup(func() { KnownHostsFile = "" })

	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	otherKey, _ := ssh.NewPublicKey(other)
	lines := []string{
		"# a comment",
		"pq.example ssh-mldsa44-ed25519 AAAAE3NzaC1tbGRzYTQ0LWVkMjU1MTkAAAAgAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=",
		knownhosts.Line([]string{"known.example"}, key),
		knownhosts.Line([]string{"changed.example"}, otherKey),
		"",
	}
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(home, ".ssh", "known_hosts")
	if err := os.WriteFile(userFile, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	asked := false
	trust := func(context.Context, string, string, string, string) bool { asked = true; return false }
	check := hostKeyCallback(context.Background(), Settings{}, trust)
	address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}
	if err := check("known.example:22", address, key); err != nil || asked {
		t.Fatalf("known host: %v, asked %v", err, asked)
	}
	if err := check("changed.example:22", address, key); err == nil || !strings.Contains(err.Error(), "changed") || !strings.Contains(err.Error(), userFile) {
		t.Fatalf("changed key: %v", err)
	}
}
