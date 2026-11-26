// SPDX-License-Identifier: MPL-2.0

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/loopy"
)

// Loopy on a server: talked to over its API, its coding task is a chat of
// the server's it follows to the end, and it is there after a restart.
func TestLoopyOnAServer(t *testing.T) {
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Role    string
				Content any
			}
		}
		json.NewDecoder(r.Body).Decode(&in)
		said, _ := in.Messages[len(in.Messages)-1].Content.(string)
		system, _ := in.Messages[0].Content.(string)
		w.Header().Set("Content-Type", "text/event-stream")
		text := func(s string) {
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", s)
		}
		switch {
		case strings.Contains(system, "You keep the memory"):
			text(`{"changes":[]}`)
		case !strings.Contains(system, "You are Loopy"):
			text("Corrigé : https://github.com/acme/app/pull/7")
		case strings.Contains(said, "corrige"):
			args, _ := json.Marshal(map[string]string{"project": "app", "title": "Corriger", "brief": "Corrige le bug."})
			quoted, _ := json.Marshal(string(args))
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"loopy_delegate\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", quoted)
		case strings.Contains(said, "coding task"):
			text("Fini : acme/app#7")
		default:
			text("Salut")
		}
	}))
	defer model.Close()
	server, call := pairedServer(t)
	root := server.root + "/app"
	mkdir(t, root)
	connection := agent.Connection{ID: "c", Name: "Local", Provider: "compatible", BaseURL: model.URL + "/v1", Enabled: true, AllModels: true}
	server.mu.Lock()
	server.projects = []string{root}
	server.connections = []agent.Connection{connection}
	server.cfg = connection.Config(root)
	server.cfg.Model = "mock"
	server.choices = map[string]agent.ModelChoice{"mock": {Connection: connection, ID: "mock"}}
	server.mu.Unlock()
	if code, _ := call("GET", "/api/loopy", nil, false); code != 401 {
		t.Fatalf("not paired: %d", code)
	}
	if code, _ := call("POST", "/api/loopy/hook/nothing", nil, false); code != 404 {
		t.Errorf("an unknown webhook: %d", code)
	}
	view := func() (v loopy.View) {
		t.Helper()
		code, body := call("GET", "/api/loopy", nil, true)
		if code != 200 || json.Unmarshal([]byte(body), &v) != nil {
			t.Fatal(code, body)
		}
		return
	}
	if v := view(); len(v.Models) != 1 || v.Model != "mock" {
		t.Errorf("%+v", v)
	}
	if code, body := call("POST", "/api/loopy/send", map[string]any{"text": "corrige le survol dans app"}, true); code != 200 {
		t.Fatal(code, body)
	}
	wait := func(what string, done func(v loopy.View) bool) loopy.View {
		t.Helper()
		for range 400 {
			if v := view(); done(v) {
				return v
			}
			time.Sleep(15 * time.Millisecond)
		}
		t.Fatalf("never: %s: %+v", what, view())
		return loopy.View{}
	}
	v := wait("a proposal", func(v loopy.View) bool { return len(v.Waiting) == 1 && !v.Running })
	if code, body := call("POST", "/api/loopy/answer", loopy.Answer{Message: v.Waiting[0], Answer: "Start"}, true); code != 200 {
		t.Fatal(code, body)
	}
	v = wait("the end", func(v loopy.View) bool {
		return len(v.Tasks) == 1 && v.Tasks[0].Status == "done" && !v.Running && strings.Contains(fmt.Sprint(v.Messages), "Fini")
	})
	if len(v.Tasks[0].PRs) != 1 || v.Tasks[0].PRs[0] != "acme/app#7" {
		t.Errorf("%+v", v.Tasks)
	}
	server.mu.Lock()
	chats := len(server.chats)
	server.mu.Unlock()
	if chats != 1 {
		t.Errorf("%d chats: Loopy's own must not be one", chats)
	}
	if code, body := call("GET", "/api/chats/loopy/panel", nil, true); code != 200 {
		t.Errorf("its panel: %d %s", code, body)
	}
	server.closeLoopy()
	server.loopyMu.Lock()
	server.loopyClosed = false
	server.loopyMu.Unlock()
	if again := view(); len(again.Messages) != len(v.Messages) || len(again.Tasks) != 1 {
		t.Errorf("after a restart: %+v", again)
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
}
