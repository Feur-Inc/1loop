// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdminSocketPairs: "1loop-server pair <id> <code>" (as docker exec runs
// it) approves a pairing through the admin socket, which only the server's
// user can open; devices and revoke work the same way (issue #404).
func TestAdminSocketPairs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// A short data directory: a Unix socket's path is limited to 108 bytes.
	dir, err := os.MkdirTemp("", "1loop-admin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0700)
	t.Setenv("ONELOOP_DATA_DIR", dir)
	server, err := newHeadlessServer(ctx, t.TempDir(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.close)
	closeAdmin, err := server.listenAdmin(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeAdmin)
	info, err := os.Stat(filepath.Join(dir, adminSocketName))
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatalf("admin socket: %v %v", info, err)
	}
	if _, err := server.listenAdmin(dir); err == nil {
		t.Fatal("a second server took over the running one's admin socket")
	}

	routes := server.routes()
	call := func(path string, body any) (int, map[string]any) {
		t.Helper()
		data, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, httptest.NewRequest("POST", path, bytes.NewReader(data)))
		out := map[string]any{}
		json.Unmarshal(recorder.Body.Bytes(), &out)
		return recorder.Code, out
	}
	status, reply := call("/api/pair", map[string]string{"device": "Phone"})
	if status != http.StatusOK {
		t.Fatalf("pair: %d %v", status, reply)
	}
	id, code := reply["pairing"].(string), reply["code"].(string)
	if command, _ := reply["command"].(string); !strings.HasSuffix(command, "1loop-server pair "+id+" "+code) {
		t.Fatalf("the pairing's command is %q", command)
	}
	if status, _ := call("/api/pair/confirm", map[string]string{"pairing": id, "code": code}); status != http.StatusConflict {
		t.Fatalf("confirmed before approval: %d", status)
	}

	var out bytes.Buffer
	if err := adminCommand(dir, []string{"pair", id, "000000x"}, &out); err == nil || !strings.Contains(out.String(), "Incorrect code") {
		t.Fatalf("a wrong code: %v %q", err, out.String())
	}
	out.Reset()
	if err := adminCommand(dir, []string{"pair", id, code}, &out); err != nil || !strings.Contains(out.String(), `Approved "Phone"`) {
		t.Fatalf("pair through the admin socket: %v %q", err, out.String())
	}
	status, reply = call("/api/pair/confirm", map[string]string{"pairing": id, "code": code})
	if status != http.StatusOK || reply["token"] == "" {
		t.Fatalf("confirm after the admin socket's approval: %d %v", status, reply)
	}

	out.Reset()
	if err := adminCommand(dir, []string{"devices"}, &out); err != nil || !strings.Contains(out.String(), "1  Phone") {
		t.Fatalf("devices: %v %q", err, out.String())
	}
	out.Reset()
	if err := adminCommand(dir, []string{"revoke", "1"}, &out); err != nil || !strings.Contains(out.String(), "Device revoked") {
		t.Fatalf("revoke: %v %q", err, out.String())
	}
	server.mu.Lock()
	devices := len(server.devices)
	server.mu.Unlock()
	if devices != 0 {
		t.Fatalf("%d devices after revoke", devices)
	}
	if err := adminCommand(dir, []string{"revoke", "1"}, &out); err == nil {
		t.Fatal("revoking an unknown device succeeded")
	}

	closeAdmin()
	if err := adminCommand(dir, []string{"devices"}, &out); err == nil || !strings.Contains(err.Error(), "no 1loop server is running") {
		t.Fatalf("without a server: %v", err)
	}
}

// TestPairCommand: the pairing dialog's command runs in the container with
// docker exec, and names the data directory when it is not the default.
func TestPairCommand(t *testing.T) {
	t.Setenv("ONELOOP_DATA_DIR", "/data")
	engine := containerEngine
	t.Cleanup(func() { containerEngine = engine })
	host, _ := os.Hostname()
	server := &headlessServer{dir: "/data"}

	containerEngine = func() string { return "docker" }
	if got, want := server.pairCommand("abc", "123456"), "docker exec "+host+" 1loop-server pair abc 123456"; got != want {
		t.Fatalf("in Docker: %q, want %q", got, want)
	}
	containerEngine = func() string { return "" }
	server.dir = "/srv/my 1loop"
	if got, want := server.pairCommand("abc", "123456"), "1loop-server -data-dir '/srv/my 1loop' pair abc 123456"; got != want {
		t.Fatalf("outside a container: %q, want %q", got, want)
	}
}
