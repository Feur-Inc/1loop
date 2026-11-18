// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A provider's refusal names the connection as the user named it and
// shows its error.message, not the JSON body; a refused key says what to
// do (#509).
func TestModelErrorsAreReadable(t *testing.T) {
	fastRetries(t)
	cfg, _ := mockModel(t, func(call int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error": {"message": "bad key (mock)"}}`)
	})
	cfg.ConnectionName = "Mock"
	_, err, _ := ask(cfg)
	if err == nil || !strings.Contains(err.Error(), "Mock refused the API key (HTTP 401): bad key (mock)") || strings.Contains(err.Error(), "{") {
		t.Fatalf("401: %v", err)
	}

	cfg, _ = mockModel(t, func(call int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": {"message": "overloaded"}}`)
	})
	_, err, _ = ask(cfg) // no name: the provider's
	if err == nil || err.Error() != "OpenAI-compatible API: HTTP 500: overloaded" {
		t.Fatalf("500: %v", err)
	}
}

// An answer with nothing in it ends the turn with a message, not silence.
func TestEmptyAnswerIsAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg, _ := mockModel(t, func(call int, w http.ResponseWriter) {
		streamAnswer(w, "")
	})
	cfg.ConnectionName, cfg.Workspace = "Mock", t.TempDir()
	inbox := Inbox{Steer: func() []Message { return nil }, Next: func() (Message, bool) { return Message{}, false }}
	err := (&App{}).Run(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, inbox, func(string, any) {})
	if err == nil || !strings.Contains(err.Error(), "Mock answered nothing") {
		t.Fatalf("empty answer: %v", err)
	}
}
