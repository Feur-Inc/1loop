// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"1loop/internal/remoteapi"
)

// The phone sees the same API from a desktop as from a server (#518):
// remoteapi.PhoneRoutes, and none of a server's own routes.
func TestRouteParity(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := &remoteServer{ui: &desktopUI{desktopApp: &desktopApp{ctx: context.Background()}}}
	missing, extra := remoteapi.RouteDifference(server.Routes(), nil)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("missing %v; not in remoteapi.PhoneRoutes: %v", missing, extra)
	}
}

// GET /api/devices and its revoke, as a server serves them: a phone finds
// itself, unpairs, and its token stops working.
func TestRemoteDevices(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ui := &desktopUI{desktopApp: &desktopApp{ctx: context.Background()}}
	ui.statePath = t.TempDir() + "/state.json"
	ui.setDevices([]remoteapi.PairedDevice{
		{Name: "Pixel", TokenHash: remoteapi.TokenHash("pixel")},
		{Name: "Tablet", TokenHash: remoteapi.TokenHash("tablet")},
	})
	server := &remoteServer{ui: ui, hub: remoteapi.NewEvents(ui.ctx)}
	routes := remoteapi.NewMux(server, false)
	call := func(method, path, token string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, req)
		out := map[string]any{}
		json.Unmarshal(recorder.Body.Bytes(), &out)
		return recorder.Code, out
	}
	code, listed := call("GET", "/api/devices", "pixel")
	devices, _ := listed["devices"].([]any)
	if code != http.StatusOK || len(devices) != 2 {
		t.Fatalf("devices: %d %v", code, listed)
	}
	var mine string
	for _, device := range devices {
		if device := device.(map[string]any); device["current"] == true {
			mine = device["id"].(string)
		}
	}
	if mine != remoteapi.DeviceID(ui.devices[0]) {
		t.Fatalf("current device %q", mine)
	}
	if code, _ := call("POST", "/api/devices/nope/revoke", "pixel"); code != http.StatusNotFound {
		t.Fatalf("revoke an unknown device: %d", code)
	}
	if code, _ := call("POST", "/api/devices/"+mine+"/revoke", "pixel"); code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := call("GET", "/api/devices", "pixel"); code != http.StatusUnauthorized {
		t.Fatalf("a revoked phone: %d", code)
	}
	if code, listed := call("GET", "/api/devices", "tablet"); code != http.StatusOK || len(listed["devices"].([]any)) != 1 {
		t.Fatalf("the other phone: %d %v", code, listed)
	}
}

// A project's variables and the naming model, as a server's routes take
// them.
func TestRemoteProjectEnvAndNaming(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ui := &desktopUI{desktopApp: &desktopApp{ctx: context.Background()}}
	ui.statePath = t.TempDir() + "/state.json"
	project := t.TempDir()
	ui.projects = []string{project}
	ui.setDevices([]remoteapi.PairedDevice{{Name: "Pixel", TokenHash: remoteapi.TokenHash("pixel")}})
	server := &remoteServer{ui: ui, hub: remoteapi.NewEvents(ui.ctx), dirty: map[*conversation]map[int]bool{}, meta: map[*conversation]bool{}}
	routes := remoteapi.NewMux(server, false)
	call := func(method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer pixel")
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, req)
		out := map[string]any{}
		json.Unmarshal(recorder.Body.Bytes(), &out)
		return recorder.Code, out
	}
	body, _ := json.Marshal(remoteapi.ProjectEnv{Path: project, Env: map[string]string{"API_URL": "http://localhost"}})
	if code, reply := call("POST", "/api/projects/env", string(body)); code != 200 || len(reply["env"].(map[string]any)) != 0 {
		t.Fatalf("save: %d %v", code, reply)
	}
	if code, reply := call("GET", "/api/projects/env?path="+project, ""); code != 200 || reply["env"].(map[string]any)["API_URL"] != "http://localhost" {
		t.Fatalf("read: %d %v", code, reply)
	}
	if code, _ := call("GET", "/api/projects/env?path=/elsewhere", ""); code != 400 {
		t.Fatalf("an unknown project: %d", code)
	}
	if code, _ := call("POST", "/api/projects/env", `{"path":"`+project+`","env":{"1 bad":"x"}}`); code != 400 {
		t.Fatalf("a bad name: %d", code)
	}
	if code, _ := call("POST", "/api/naming-model", `{"model":{"connectionId":"none","model":"gone"}}`); code != 400 {
		t.Fatalf("an unavailable naming model: %d", code)
	}
	if code, _ := call("POST", "/api/naming-model", `{"model":null}`); code != 200 || ui.namingModel != nil {
		t.Fatalf("no naming model: %d", code)
	}
}
