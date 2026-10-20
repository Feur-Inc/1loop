package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// mockModel answers /chat/completions with handler, counting the calls.
func mockModel(t *testing.T, handler func(call int, w http.ResponseWriter)) (Config, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(int(calls.Add(1)), w)
	}))
	t.Cleanup(server.Close)
	return Config{Provider: "compatible", BaseURL: server.URL + "/v1", Model: "mock"}, calls
}

func streamAnswer(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", text)
	fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

func fastRetries(t *testing.T) {
	t.Helper()
	base, idle := retryBase, streamIdleTimeout
	retryBase, streamIdleTimeout = 10*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { retryBase, streamIdleTimeout = base, idle })
}

func ask(cfg Config) (Message, error, []string) {
	var notices []string
	out, err := completion(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, false, func(kind string, value any) {
		if kind == "notice" {
			notices = append(notices, value.(string))
		}
	})
	return out, err, notices
}

func TestRetryTemporaryErrors(t *testing.T) {
	fastRetries(t)
	cfg, calls := mockModel(t, func(call int, w http.ResponseWriter) {
		if call <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		streamAnswer(w, "hello")
	})
	out, err, notices := ask(cfg)
	if err != nil || out.Content != "hello" || calls.Load() != 3 || len(notices) != 2 || notices[0] != "Reconnecting (1/4)…" {
		t.Fatalf("out %q err %v calls %d notices %v", out.Content, err, calls.Load(), notices)
	}
}

func TestRetryAfterAndQuota(t *testing.T) {
	fastRetries(t)
	cfg, calls := mockModel(t, func(call int, w http.ResponseWriter) {
		if call == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		streamAnswer(w, "after the wait")
	})
	started := time.Now()
	if out, err, _ := ask(cfg); err != nil || out.Content != "after the wait" || time.Since(started) < time.Second {
		t.Fatalf("Retry-After: %q %v after %s", out.Content, err, time.Since(started))
	}

	// A used-up plan, or a wait over a minute, is told at once.
	cfg, calls = mockModel(t, func(call int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"type":"usage_limit_reached"}}`)
	})
	if _, err, _ := ask(cfg); err == nil || calls.Load() != 1 {
		t.Fatalf("usage limit retried: %v after %d calls", err, calls.Load())
	}
	cfg, calls = mockModel(t, func(call int, w http.ResponseWriter) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err, _ := ask(cfg); err == nil || calls.Load() != 1 {
		t.Fatalf("an hour's wait retried: %v after %d calls", err, calls.Load())
	}
}

func TestNoRetryOnRefusal(t *testing.T) {
	fastRetries(t)
	cfg, calls := mockModel(t, func(call int, w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "bad request")
	})
	if _, err, _ := ask(cfg); err == nil || !strings.Contains(err.Error(), "HTTP 400") || calls.Load() != 1 {
		t.Fatalf("400: %v after %d calls", err, calls.Load())
	}
}

func TestSilentStreamIsAskedAgain(t *testing.T) {
	fastRetries(t)
	cfg, calls := mockModel(t, func(call int, w http.ResponseWriter) {
		if call == 1 { // headers, then nothing: the connection hangs
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			time.Sleep(time.Second)
			return
		}
		streamAnswer(w, "second try")
	})
	out, err, notices := ask(cfg)
	if err != nil || out.Content != "second try" || calls.Load() != 2 || len(notices) != 1 || !strings.HasPrefix(notices[0], "Connection lost, reconnecting (1/5)") {
		t.Fatalf("out %q err %v calls %d notices %v", out.Content, err, calls.Load(), notices)
	}
}

func TestShownTextIsNotAskedAgain(t *testing.T) {
	fastRetries(t)
	cfg, calls := mockModel(t, func(call int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half an answ\"}}]}\n\n")
		// The connection drops before the end.
	})
	if _, err, _ := ask(cfg); err == nil || calls.Load() != 1 {
		t.Fatalf("a broken stream with text on screen was asked again: %v after %d calls", err, calls.Load())
	}
}
