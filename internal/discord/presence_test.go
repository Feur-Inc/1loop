// SPDX-License-Identifier: MPL-2.0

package discord

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeDiscord answers like the Discord app and hands over each command.
func fakeDiscord(t *testing.T, server net.Conn, commands chan<- map[string]any) {
	t.Helper()
	readFrame := func() (uint32, map[string]any, bool) {
		var header [8]byte
		if _, err := io.ReadFull(server, header[:]); err != nil {
			return 0, nil, false
		}
		body := make([]byte, binary.LittleEndian.Uint32(header[4:]))
		if _, err := io.ReadFull(server, body); err != nil {
			return 0, nil, false
		}
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		return binary.LittleEndian.Uint32(header[:4]), payload, true
	}
	writeFrame := func(op uint32, payload any) {
		body, _ := json.Marshal(payload)
		header := make([]byte, 8)
		binary.LittleEndian.PutUint32(header[:4], op)
		binary.LittleEndian.PutUint32(header[4:], uint32(len(body)))
		_, _ = server.Write(append(header, body...))
	}
	go func() {
		op, hello, ok := readFrame()
		if !ok || op != opHandshake || hello["client_id"] != "123" {
			t.Errorf("handshake: op %d, %v", op, hello)
			return
		}
		writeFrame(opPing, map[string]any{})
		writeFrame(opFrame, map[string]any{"cmd": "DISPATCH", "evt": "READY", "data": map[string]any{}})
		for {
			op, command, ok := readFrame()
			if !ok || op == opClose {
				close(commands)
				return
			}
			if op == opPong {
				continue
			}
			commands <- command
			writeFrame(opFrame, map[string]any{"cmd": command["cmd"], "nonce": command["nonce"], "data": map[string]any{}})
		}
	}()
}

func TestPresenceSetsAndClears(t *testing.T) {
	client, server := net.Pipe()
	commands := make(chan map[string]any, 4)
	fakeDiscord(t, server, commands)
	dialer = func() (io.ReadWriteCloser, error) { return client, nil }
	defer func() { dialer = dial }()

	presence := New("123", t.Logf)
	start := time.UnixMilli(1_700_000_000_000)
	presence.Set(&Activity{Details: "Working with Opus 5.5", State: "x", LargeImage: "https://example.com/think.gif",
		LargeText: "Thinking", Start: start, Buttons: []Button{{"Get 1loop", "https://github.com/Feur-Inc/1loop"}}})
	command := next(t, commands)
	activity := command["args"].(map[string]any)["activity"].(map[string]any)
	if command["cmd"] != "SET_ACTIVITY" || activity["details"] != "Working with Opus 5.5" {
		t.Fatalf("command %v", command)
	}
	if _, ok := activity["state"]; ok {
		t.Errorf("a one-letter state is sent: Discord refuses the whole activity for it")
	}
	if activity["assets"].(map[string]any)["large_image"] != "https://example.com/think.gif" ||
		activity["timestamps"].(map[string]any)["start"] != float64(start.UnixMilli()) {
		t.Errorf("activity %v", activity)
	}
	presence.Close()
	command = next(t, commands)
	if _, ok := command["args"].(map[string]any)["activity"]; ok {
		t.Errorf("closing does not clear the activity: %v", command)
	}
	if _, open := <-commands; open {
		t.Errorf("the connection stays open after Close")
	}
}

func next(t *testing.T, commands <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case command := <-commands:
		return command
	case <-time.After(3 * time.Second):
		t.Fatal("no command reached Discord")
		return nil
	}
}

func TestClip(t *testing.T) {
	long := strings.Repeat("é", 200)
	if got := clip(long, 128); len([]rune(got)) != 128 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip: %d runes", len([]rune(got)))
	}
	if got := clip("  Running\n go test  ", 128); got != "Running go test" {
		t.Errorf("clip spaces: %q", got)
	}
}
