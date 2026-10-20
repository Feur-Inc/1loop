package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"1loop/internal/core"
	"1loop/internal/remoteapi"
)

func TestPhoneSharing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Local phone mode requires Unix owner-only token permissions")
	}
	dir, workspace := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, workspace, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	localToken := core.Identifier()
	tokenFile := filepath.Join(dir, "client-token")
	if err := os.WriteFile(tokenFile, []byte(localToken), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.authorizeLocalClient(tokenFile, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	certificate, err := remoteapi.Certificate(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	server.handler = server.routes()
	server.fingerprint = remoteapi.Fingerprint(certificate)
	server.tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	local := httptest.NewServer(server.handler) // the phone app's loopback listener
	defer local.Close()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	call := func(base, method, path, token string, body any) (int, map[string]any) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(data))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		out := map[string]any{}
		json.Unmarshal(raw, &out)
		return response.StatusCode, out
	}

	// Off at the start: nothing listens on the network; only the app may turn it on.
	if code, state := call(local.URL, "GET", "/api/local/sharing", localToken, nil); code != 200 || state["enabled"] != false {
		t.Fatalf("initial state: %d %v", code, state)
	}
	if code, _ := call(local.URL, "POST", "/api/local/sharing", "", map[string]bool{"enabled": true}); code != 403 {
		t.Fatalf("sharing without the app's token: %d", code)
	}
	code, state := call(local.URL, "POST", "/api/local/sharing", localToken, map[string]bool{"enabled": true})
	if code != 200 || state["enabled"] != true {
		t.Fatalf("enable: %d %v", code, state)
	}
	network := fmt.Sprintf("https://127.0.0.1:%d", server.sharingPort)
	defer call(local.URL, "POST", "/api/local/sharing", localToken, map[string]bool{"enabled": false})

	// A device pairs: it shows a code, the phone's user types it.
	code, pairing := call(network, "POST", "/api/pair", "", map[string]string{"device": "Laptop"})
	if code != 200 || pairing["console"] != true || pairing["phone"] != true {
		t.Fatalf("pair: %d %v", code, pairing)
	}
	id, shown := pairing["pairing"].(string), pairing["code"].(string)
	if _, state := call(local.URL, "GET", "/api/local/sharing", localToken, nil); len(state["pending"].([]any)) != 1 {
		t.Fatalf("pending not listed: %v", state)
	} else if expires := state["pending"].([]any)[0].(map[string]any)["expiresAt"].(float64); expires <= float64(time.Now().UnixMilli()) {
		t.Fatalf("pending pairing has no future expiry: %v", state)
	}
	if code, _ := call(network, "POST", "/api/pair/confirm", "", map[string]string{"pairing": id, "code": shown}); code != 409 {
		t.Fatalf("confirmed before approval: %d", code)
	}
	wrongCode := "000000"
	if shown == wrongCode {
		wrongCode = "000001"
	}
	if code, _ := call(local.URL, "POST", "/api/local/pairings/"+id, localToken, map[string]any{"code": wrongCode, "approve": true}); code != 403 {
		t.Fatalf("approved with the wrong code: %d", code)
	}
	if code, _ := call(local.URL, "POST", "/api/local/pairings/"+id, localToken, map[string]any{"code": shown, "approve": true}); code != 200 {
		t.Fatalf("approve: %d", code)
	}
	code, reply := call(network, "POST", "/api/pair/confirm", "", map[string]string{"pairing": id, "code": shown})
	deviceToken, _ := reply["token"].(string)
	if code != 200 || deviceToken == "" {
		t.Fatalf("confirm: %d %v", code, reply)
	}

	// The device drives the chats, never the phone app's own routes.
	if code, _ := call(network, "GET", "/api/state", deviceToken, nil); code != 200 {
		t.Fatalf("device state: %d", code)
	}
	for _, path := range []string{"/api/local/sharing", "/api/local/connections"} {
		if code, _ := call(network, "GET", path, deviceToken, nil); code != 403 {
			t.Fatalf("network %s: %d", path, code)
		}
		if code, _ := call(local.URL, "GET", path, deviceToken, nil); code != 403 {
			t.Fatalf("device token on loopback %s: %d", path, code)
		}
	}
	if code, _ := call(network, "GET", "/api/local/sharing", localToken, nil); code != 403 {
		t.Fatalf("the app's routes over the network: %d", code)
	}

	// Removed from the phone: its token stops working.
	_, state = call(local.URL, "GET", "/api/local/sharing", localToken, nil)
	if devices := state["devices"].([]any); len(devices) != 1 || devices[0].(map[string]any)["name"] != "Laptop" {
		t.Fatalf("devices: %v", state["devices"])
	}
	if code, _ := call(local.URL, "POST", "/api/local/devices/0/revoke", localToken, nil); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := call(network, "GET", "/api/state", deviceToken, nil); code != 401 {
		t.Fatalf("revoked device: %d", code)
	}

	// Turned off: the network listener closes.
	call(local.URL, "POST", "/api/local/sharing", localToken, map[string]bool{"enabled": false})
	time.Sleep(200 * time.Millisecond)
	if _, err := client.Get(network + "/api/info"); err == nil {
		t.Fatal("still listening on the network after sharing was turned off")
	}

	// Expired or locked-out requests must disappear from the phone's dialog.
	call(local.URL, "POST", "/api/local/sharing", localToken, map[string]bool{"enabled": true})
	network = fmt.Sprintf("https://127.0.0.1:%d", server.sharingPort)
	_, pairing = call(network, "POST", "/api/pair", "", map[string]string{"device": "Expired laptop"})
	id = pairing["pairing"].(string)
	server.mu.Lock()
	server.pairs[id].Expires = time.Now().Add(-time.Second)
	server.mu.Unlock()
	if _, state := call(local.URL, "GET", "/api/local/sharing", localToken, nil); len(state["pending"].([]any)) != 0 {
		t.Fatalf("expired pairing still pending: %v", state)
	}
	if code, _ := call(local.URL, "POST", "/api/local/pairings/"+id, localToken, map[string]any{"code": pairing["code"], "approve": true}); code != 404 {
		t.Fatalf("approved an expired pairing: %d", code)
	}
	_, pairing = call(network, "POST", "/api/pair", "", map[string]string{"device": "Locked laptop"})
	id, shown = pairing["pairing"].(string), pairing["code"].(string)
	wrongCode = "000000"
	if shown == wrongCode {
		wrongCode = "000001"
	}
	for attempt := 0; attempt < 5; attempt++ {
		if code, _ := call(local.URL, "POST", "/api/local/pairings/"+id, localToken, map[string]any{"code": wrongCode, "approve": true}); code != 403 {
			t.Fatalf("wrong pairing attempt %d: %d", attempt, code)
		}
	}
	if _, state := call(local.URL, "GET", "/api/local/sharing", localToken, nil); len(state["pending"].([]any)) != 0 {
		t.Fatalf("locked-out pairing still pending: %v", state)
	}
	server.close()
	if _, err := client.Get(network + "/api/info"); err == nil {
		t.Fatal("still listening on the network after the engine closed")
	}
}
