// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitsNote(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&limitsHTTPError{Status: 401}, "Sign in again"},
		{&limitsHTTPError{Status: 403}, "This login can't read usage"},
		{&limitsHTTPError{Status: 429, RetryAfter: 2864 * time.Second}, "Rate limited, try again in 48 min"},
		{&limitsHTTPError{Status: 429, RetryAfter: 30 * time.Second}, "Rate limited, try again in a minute"},
		{&limitsHTTPError{Status: 429, RetryAfter: 90 * time.Minute}, "Rate limited, try again in 1 h 30 min"},
		{&limitsHTTPError{Status: 429, RetryAfter: 2 * time.Hour}, "Rate limited, try again in 2 h"},
		{&limitsHTTPError{Status: 429}, "Rate limited, try again in a few minutes"},
		{&limitsHTTPError{Status: 503}, "Provider unavailable, try later"},
		{&limitsHTTPError{Status: 418}, "Limits unavailable (HTTP 418)"},
		{fs.ErrNotExist, "Sign in again"},
		{errClaudeSignedOut, "Sign in again"},
		{errClaudeExpired, "Session expired, send a message to renew it"},
		{fs.ErrPermission, "Can't read the login files"},
		{context.DeadlineExceeded, "Can't reach the provider"},
		{&json.SyntaxError{}, "Unexpected answer from the provider"},
		{errors.New("boom"), "Limits unavailable"},
	} {
		if got := LimitsNote(tc.err); got != tc.want {
			t.Errorf("%v: %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	for value, want := range map[string]time.Duration{
		"2864": 2864 * time.Second,
		"":     0,
		"soon": 0,
		"-5":   0,
		now.Add(time.Hour).Format(http.TimeFormat):  time.Hour,
		now.Add(-time.Hour).Format(http.TimeFormat): 0,
	} {
		if got := retryAfter(value, now); got != want {
			t.Errorf("Retry-After %q: %v, want %v", value, got, want)
		}
	}
}

func TestLimitsAfterError(t *testing.T) {
	checked := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	last := AccountLimits{Windows: []AccountLimitWindow{NewAccountLimitWindow("5h", 70, time.Time{})}, Checked: checked}
	got := LimitsAfterError(last, &limitsHTTPError{Status: 429, RetryAfter: time.Hour})
	if len(got.Windows) != 1 || got.Windows[0].Remaining() != 70 || !got.Checked.Equal(checked) || got.Note != "Rate limited, try again in 1 h" {
		t.Errorf("with numbers: %+v", got)
	}
	got = LimitsAfterError(AccountLimits{}, &limitsHTTPError{Status: 403})
	if len(got.Windows) != 0 || got.Note != "This login can't read usage" {
		t.Errorf("without numbers: %+v", got)
	}
}

func writeClaudeCredentials(t *testing.T, dir, access, refresh string, expires time.Time) {
	t.Helper()
	expiresAt, _ := json.Marshal(expires.UnixMilli())
	creds := `{"other":1,"claudeAiOauth":{"accessToken":"` + access + `","refreshToken":"` + refresh + `","expiresAt":` + string(expiresAt) + `,"scopes":["x"]}}`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
}

// systemClaudeLogin makes the system Claude Code login a fresh folder.
func systemClaudeLogin(t *testing.T, access, refresh string, expires time.Time) Connection {
	claudeAccountsHome(t)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	writeClaudeCredentials(t, dir, access, refresh, expires)
	return Connection{ID: "sys", Provider: "claudecode", Enabled: true}
}

// claudeServer stands for Claude's usage and refresh endpoints.
func claudeServer(t *testing.T, usage http.HandlerFunc) (usageHits, refreshHits *atomic.Int32) {
	t.Helper()
	usageHits, refreshHits = new(atomic.Int32), new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			refreshHits.Add(1)
			w.Write([]byte(`{"access_token":"new-a","refresh_token":"new-r","expires_in":3600}`))
			return
		}
		usageHits.Add(1)
		usage(w, r)
	}))
	oldUsage, oldRefresh := claudeUsageURL, claudeRefreshURL
	claudeUsageURL, claudeRefreshURL = server.URL+"/usage", server.URL+"/token"
	t.Cleanup(func() {
		claudeUsageURL, claudeRefreshURL = oldUsage, oldRefresh
		server.Close()
	})
	return usageHits, refreshHits
}

func TestClaudeLimitsOwnLoginRefreshesExpiredToken(t *testing.T) {
	claudeAccountsHome(t)
	conn := Connection{ID: "own1", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin, Enabled: true}
	writeClaudeCredentials(t, conn.ClaudeHome(), "old-a", "old-r", time.Now().Add(-time.Hour))
	_, refreshed := claudeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer new-a" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"five_hour":{"utilization":25,"resets_at":"2030-01-01T00:00:00Z"}}`))
	})
	limits, err := (&App{}).ConnectionLimits(context.Background(), conn, "")
	if err != nil || len(limits.Windows) != 1 || limits.Windows[0].Remaining() != 75 || limits.Note != "" {
		t.Fatalf("limits %+v err %v", limits, err)
	}
	if refreshed.Load() != 1 {
		t.Errorf("refreshed %d times, want 1", refreshed.Load())
	}
	data, _ := os.ReadFile(filepath.Join(conn.ClaudeHome(), ".credentials.json"))
	for _, want := range []string{"new-a", "new-r", `"other"`, `"scopes"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("saved credentials lack %s", want)
		}
	}
	if _, err := os.Stat(filepath.Join(conn.ClaudeHome(), ".credentials.json.1loop.tmp")); err == nil {
		t.Error("temporary file left behind")
	}
}

func TestClaudeLimitsOwnLoginRefreshRevoked(t *testing.T) {
	claudeAccountsHome(t)
	conn := Connection{ID: "own2", Provider: "claudecode", ClaudeLogin: ClaudeOwnLogin, Enabled: true}
	writeClaudeCredentials(t, conn.ClaudeHome(), "old-a", "old-r", time.Now().Add(-time.Hour))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) }))
	defer server.Close()
	old := claudeRefreshURL
	claudeRefreshURL = server.URL
	defer func() { claudeRefreshURL = old }()
	_, err := (&App{}).ConnectionLimits(context.Background(), conn, "")
	if got := LimitsNote(err); got != "Sign in again" {
		t.Errorf("%q (%v)", got, err)
	}
	data, _ := os.ReadFile(filepath.Join(conn.ClaudeHome(), ".credentials.json"))
	if !strings.Contains(string(data), "old-r") {
		t.Error("credentials changed though the refresh failed")
	}
}

func TestClaudeLimitsSystemLoginIsNotRefreshed(t *testing.T) {
	conn := systemClaudeLogin(t, "a", "r", time.Now().Add(-time.Hour))
	usage, refreshed := claudeServer(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := (&App{}).ConnectionLimits(context.Background(), conn, "")
	if got := LimitsNote(err); got != "Session expired, send a message to renew it" {
		t.Errorf("%q (%v)", got, err)
	}
	if usage.Load() != 0 || refreshed.Load() != 0 {
		t.Errorf("the system login's refresh token was used (%d usage, %d refresh calls)", usage.Load(), refreshed.Load())
	}
}

func TestClaudeLimitsErrors(t *testing.T) {
	valid := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name   string
		setup  func(t *testing.T) Connection
		status int
		body   string
		want   string
	}{
		{"no file", func(t *testing.T) Connection {
			claudeAccountsHome(t)
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			return Connection{ID: "sys", Provider: "claudecode", Enabled: true}
		}, 200, "", "Sign in again"},
		{"403", func(t *testing.T) Connection { return systemClaudeLogin(t, "a", "r", valid) }, 403, "", "This login can't read usage"},
		{"401 on a system login", func(t *testing.T) Connection { return systemClaudeLogin(t, "a", "r", valid) }, 401, "", "Sign in again"},
		{"500", func(t *testing.T) Connection { return systemClaudeLogin(t, "a", "r", valid) }, 500, "", "Provider unavailable, try later"},
		{"expired, signed out", func(t *testing.T) Connection { return systemClaudeLogin(t, "", "", valid.Add(-2*time.Hour)) }, 200, "", "Sign in again"},
		{"bad json", func(t *testing.T) Connection { return systemClaudeLogin(t, "a", "r", valid) }, 200, "not json", "Unexpected answer from the provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := tc.setup(t)
			claudeServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			_, err := (&App{}).ConnectionLimits(context.Background(), conn, "")
			if got := LimitsNote(err); got != tc.want {
				t.Errorf("%q (%v), want %q", got, err, tc.want)
			}
		})
	}
}

// Claude says "too many requests" with a long Retry-After for a login: the
// reason says how long, and the login (every connection on it) is not asked
// again before then.
func TestClaudeLimitsRateLimitedPause(t *testing.T) {
	conn := systemClaudeLogin(t, "a", "r", time.Now().Add(time.Hour))
	other := conn
	other.ID = "sys2" // a second connection on the same system login
	usage, _ := claudeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2864")
		w.WriteHeader(429)
	})
	_, err := (&App{}).ConnectionLimits(context.Background(), conn, "")
	if got := LimitsNote(err); got != "Rate limited, try again in 48 min" {
		t.Errorf("first read: %q (%v)", got, err)
	}
	for _, c := range []Connection{conn, other} {
		_, err = (&App{}).ConnectionLimits(context.Background(), c, "")
		if got := LimitsNote(err); !strings.HasPrefix(got, "Rate limited, try again in ") {
			t.Errorf("during the pause: %q (%v)", got, err)
		}
	}
	if usage.Load() != 1 {
		t.Errorf("Claude was asked %d times, want 1", usage.Load())
	}
}

func TestClaudeLimitsSharedRead(t *testing.T) {
	conn := systemClaudeLogin(t, "a", "r", time.Now().Add(time.Hour))
	other := conn
	other.ID = "sys2"
	usage, _ := claudeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"five_hour":{"utilization":10,"resets_at":"2030-01-01T00:00:00Z"}}`))
	})
	for _, c := range []Connection{conn, other, conn} {
		limits, err := (&App{}).ConnectionLimits(context.Background(), c, "")
		if err != nil || len(limits.Windows) != 1 || limits.Windows[0].Remaining() != 90 {
			t.Fatalf("limits %+v err %v", limits, err)
		}
	}
	if usage.Load() != 1 {
		t.Errorf("Claude was asked %d times for one login, want 1", usage.Load())
	}
}
