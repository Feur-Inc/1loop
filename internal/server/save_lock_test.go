// SPDX-License-Identifier: MPL-2.0

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"1loop/internal/remoteapi"
)

// Unpairing a device writes the state without holding server.mu: requests
// and agent events go on while it is written (#520).
func TestSaveDoesNotHoldTheLock(t *testing.T) {
	server, _, _ := testServer(t)
	server.mu.Lock()
	server.devices = append(server.devices, remoteapi.PairedDevice{Name: "old phone", TokenHash: "gone"})
	index := len(server.devices) - 1
	server.mu.Unlock()

	server.saveLock.Lock() // the write waits here, as a slow disk would
	done := make(chan error, 1)
	go func() {
		server.mu.Lock()
		defer server.mu.Unlock()
		done <- server.removeDeviceLocked(index)
	}()
	// The lock must be free while the device is already gone and its
	// write still waits.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if server.mu.TryLock() {
			removed := len(server.devices) == index
			server.mu.Unlock()
			if removed {
				break
			}
		}
		if time.Now().After(deadline) {
			server.saveLock.Unlock()
			<-done
			t.Fatal("server.mu stayed held while the state was written")
		}
		time.Sleep(time.Millisecond)
	}
	server.saveLock.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(server.dir, "state.json"))
	if err != nil || strings.Contains(string(data), "old phone") {
		t.Fatalf("saved state: %v %s", err, data)
	}
}

func TestRestoredChats(t *testing.T) {
	a, b, c, created := &serverChat{ID: "a"}, &serverChat{ID: "b"}, &serverChat{ID: "c"}, &serverChat{ID: "new"}
	got := restoredChats([]*serverChat{a, b, c}, []*serverChat{created, a, c}, []*serverChat{b})
	var ids []string
	for _, chat := range got {
		ids = append(ids, chat.ID)
	}
	if strings.Join(ids, ",") != "new,a,b,c" {
		t.Fatalf("restored %v", ids)
	}
}
