// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"context"
	"testing"
)

// A file name or path is not sent out as a web request, and only this
// machine gets plain http (#194).
func TestURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                        "about:blank",
		"example.com":             "https://example.com",
		"example.com/README.md":   "https://example.com/README.md",
		"localhost:3000/app":      "http://localhost:3000/app",
		"localhost":               "http://localhost",
		"app.localhost:8080":      "http://app.localhost:8080",
		"127.0.0.1:8000":          "http://127.0.0.1:8000",
		"[::1]:5173":              "http://[::1]:5173",
		"localhost.evil.example":  "https://localhost.evil.example",
		"127.attacker.example":    "https://127.attacker.example",
		"intranet:8080":           "https://intranet:8080",
		"https://README.md":       "https://README.md",
		"HTTPS://Example.com":     "HTTPS://Example.com",
		"file:/etc/hostname":      "file:/etc/hostname",
		"/etc/hostname":           "file:///etc/hostname",
		"about:blank":             "about:blank",
		"data:text/html,hi":       "data:text/html,hi",
		"github.io":               "https://github.io",
		"news.example.md/article": "https://news.example.md/article",
	} {
		got, err := URL(raw)
		if err != nil || got != want {
			t.Errorf("URL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"README.md", "index.html", "./index.html", "../x", "~/notes.txt", `docs\a.md`, "docs/README.md", "main.go", "javascript:alert(1)", "mailto:a@example.com"} {
		if got, err := URL(raw); err == nil {
			t.Errorf("URL(%q) = %q, want an error", raw, got)
		}
	}
}

// Navigating to nothing is refused instead of blanking the page (#208).
func TestNavigateEmpty(t *testing.T) {
	if err := (&Page{}).Navigate(context.Background(), "navigate", "  "); err == nil {
		t.Fatal("empty address accepted")
	}
}
