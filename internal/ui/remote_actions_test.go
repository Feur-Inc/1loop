// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// untilDrained waits for done, running what goroutines handed to the window.
func untilDrained(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		test.Drain()
		time.Sleep(5 * time.Millisecond)
	}
}

// recordedStatus is the line under the conversation, as a test reads it.
type recordedStatus struct{ text string }

func (status *recordedStatus) SetText(text string) { status.text = text }

// heldServer answers each POST once the test lets it, with status, and
// keeps the bodies it got by path.
type heldServer struct {
	mu      sync.Mutex
	release chan struct{}
	status  int
	bodies  map[string][]map[string]any
}

func newHeldServer(status int) *heldServer {
	return &heldServer{release: make(chan struct{}), status: status, bodies: map[string][]map[string]any{}}
}

func (held *heldServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Write([]byte("{}"))
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	held.mu.Lock()
	held.bodies[r.URL.Path] = append(held.bodies[r.URL.Path], body)
	held.mu.Unlock()
	<-held.release
	if held.status != 200 {
		w.WriteHeader(held.status)
		w.Write([]byte(`{"error":"The server refused it"}`))
		return
	}
	w.Write([]byte("{}"))
}

func (held *heldServer) count(path string) int {
	held.mu.Lock()
	defer held.mu.Unlock()
	return len(held.bodies[path])
}

// Stop clicked twice while the first request is on its way sends it once:
// the button is disabled until the server answers, then comes back.
func TestServerActionSentOncePerClick(t *testing.T) {
	held := newHeldServer(200)
	ui, remote, _ := serverWindow(t, held.handle)
	openRead(remote, "c1", 1)
	remote.stop()
	untilDrained(t, "the first stop to reach the server", func() bool { return held.count("/api/stop") == 1 })
	if !ui.stop.disabled {
		t.Fatal("Stop is not disabled while its request is on its way")
	}
	remote.stop()
	remote.stop()
	time.Sleep(50 * time.Millisecond)
	if n := held.count("/api/stop"); n != 1 {
		t.Fatalf("%d stops sent for three clicks while the first was on its way", n)
	}
	close(held.release)
	untilDrained(t, "Stop to come back", func() bool { return !ui.stop.disabled })
	if remote.sending("stop:c1") {
		t.Fatal("the stop is still marked as on its way")
	}
	remote.stop() // answered: a new click sends again
	untilDrained(t, "the second stop", func() bool { return held.count("/api/stop") == 2 })
}

// The model chosen while the first choice is on its way is sent once it
// is answered: the last one wins, the chip is disabled meanwhile.
func TestServerModelChoiceKeepsTheLatest(t *testing.T) {
	held := newHeldServer(200)
	ui, remote, _ := serverWindow(t, held.handle)
	openRead(remote, "c1", 1)
	choose := func(label string) {
		remote.act("/api/model", map[string]string{"chat": "c1", "label": label}, remoteAction{key: "model:label:c1", control: ui.model, latest: true})
	}
	choose("first")
	untilDrained(t, "the first choice to reach the server", func() bool { return held.count("/api/model") == 1 })
	choose("second")
	choose("third")
	if !ui.model.row.disabled {
		t.Fatal("the model chip is not disabled while the choice is on its way")
	}
	close(held.release)
	untilDrained(t, "the last choice", func() bool { return held.count("/api/model") == 2 })
	untilDrained(t, "the chip to come back", func() bool { return !ui.model.row.disabled && !remote.sending("model:label:c1") })
	held.mu.Lock()
	defer held.mu.Unlock()
	if last := held.bodies["/api/model"][1]["label"]; last != "third" {
		t.Fatalf("sent after the first: %v, want the last choice", last)
	}
}

// An action the server refuses says why in a notice, not only in the line
// under the conversation.
func TestServerActionErrorShown(t *testing.T) {
	held := newHeldServer(http.StatusConflict)
	close(held.release)
	ui, remote, _ := serverWindow(t, held.handle)
	status := &recordedStatus{}
	remote.status = status
	openRead(remote, "c1", 1)
	overlays := len(ui.window.Canvas().Overlays().List())
	remote.action("/api/approval-mode", map[string]string{"mode": "yolo"})
	untilDrained(t, "the refusal", func() bool { return status.text != "" })
	if status.text != "The server refused it" {
		t.Fatalf("status line: %q", status.text)
	}
	if len(ui.window.Canvas().Overlays().List()) <= overlays {
		t.Fatal("the refusal shows in no notice")
	}
}

// Send pressed again while the message is on its way does nothing: the
// text typed meanwhile stays in the composer.
func TestServerSendOncePerClick(t *testing.T) {
	held := newHeldServer(200)
	ui, remote, _ := serverWindow(t, held.handle)
	openRead(remote, "c1", 1)
	ui.prompt.SetText("first")
	remote.submit("send")
	untilDrained(t, "the message to reach the server", func() bool { return held.count("/api/send") == 1 })
	if !ui.send.disabled {
		t.Fatal("Send is not disabled while the message is on its way")
	}
	ui.prompt.SetText("second")
	remote.submit("send")
	time.Sleep(50 * time.Millisecond)
	if held.count("/api/send") != 1 || ui.prompt.Text != "second" {
		t.Fatalf("%d sends, composer %q", held.count("/api/send"), ui.prompt.Text)
	}
	close(held.release)
	untilDrained(t, "Send to come back", func() bool { return !ui.send.disabled })
}
