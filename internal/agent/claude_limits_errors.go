// SPDX-License-Identifier: MPL-2.0

package agent

// Why an account's limits cannot be read, in plain words; the pause after
// Claude says "too many requests"; and the refresh of an own-login Claude
// Code access token.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// limitsHTTPError is a provider answering something other than 200.
type limitsHTTPError struct {
	Status int
	// RetryAfter is what the provider asked to wait (Retry-After), 0 when
	// it did not say.
	RetryAfter time.Duration
}

func (err *limitsHTTPError) Error() string { return fmt.Sprintf("HTTP %d", err.Status) }

var (
	// errClaudeSignedOut: no usable Claude login in the folder.
	errClaudeSignedOut = errors.New("claude: signed out")
	// errClaudeExpired: the system login's access token has expired. Claude
	// Code renews it the next time it runs; 1loop does not, because the
	// refresh token changes at each use and the user's own terminal shares it.
	errClaudeExpired = errors.New("claude: session expired")
)

// LimitsNote is what to tell the user when an account's limits could not be
// read: the reason in plain words, never a token or a raw error.
func LimitsNote(err error) string {
	var httpErr *limitsHTTPError
	var netErr net.Error
	var syntax *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errClaudeExpired):
		return "Session expired, send a message to renew it"
	case errors.Is(err, errClaudeSignedOut), errors.Is(err, fs.ErrNotExist):
		return "Sign in again"
	case errors.As(err, &httpErr):
		switch httpErr.Status {
		case http.StatusUnauthorized:
			return "Sign in again"
		case http.StatusForbidden:
			return "This login can't read usage"
		case http.StatusTooManyRequests:
			return "Rate limited, try again " + retryIn(httpErr.RetryAfter)
		}
		if httpErr.Status >= 500 {
			return "Provider unavailable, try later"
		}
		return fmt.Sprintf("Limits unavailable (HTTP %d)", httpErr.Status)
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr):
		return "Can't reach the provider"
	case errors.As(err, &syntax), errors.As(err, &typeErr), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return "Unexpected answer from the provider"
	case errors.Is(err, fs.ErrPermission):
		return "Can't read the login files"
	}
	return "Limits unavailable"
}

// retryIn says when to try again: "in 48 min", "in 1 h 20 min".
func retryIn(wait time.Duration) string {
	if wait <= 0 {
		return "in a few minutes"
	}
	minutes := int((wait + time.Minute - 1) / time.Minute)
	switch {
	case minutes <= 1:
		return "in a minute"
	case minutes < 60:
		return fmt.Sprintf("in %d min", minutes)
	case minutes%60 == 0:
		return fmt.Sprintf("in %d h", minutes/60)
	}
	return fmt.Sprintf("in %d h %d min", minutes/60, minutes%60)
}

// LimitsAfterError is what to show when reading an account's limits failed:
// the last good numbers when there are any, with the reason beside them.
func LimitsAfterError(last AccountLimits, err error) AccountLimits {
	if len(last.Windows) > 0 {
		last.Note = LimitsNote(err)
		return last
	}
	return AccountLimits{Note: LimitsNote(err)}
}

// Connections that share a Claude login share its usage numbers: read at
// most every claudeLimitsEvery, and not at all while Claude asks to wait.
const claudeLimitsEvery = 30 * time.Second

type claudeLimitsState struct {
	mu     sync.Mutex
	at     time.Time
	limits AccountLimits
	until  time.Time // Claude asked not to be called before
}

var claudeLimitsRead struct {
	mu    sync.Mutex
	byDir map[string]*claudeLimitsState
}

// claudeLimitsShared runs read for the login folder dir, answering from the
// last read when it is fresh, or from the pause Claude asked for.
func claudeLimitsShared(dir string, read func() (AccountLimits, error)) (AccountLimits, error) {
	claudeLimitsRead.mu.Lock()
	if claudeLimitsRead.byDir == nil {
		claudeLimitsRead.byDir = map[string]*claudeLimitsState{}
	}
	state := claudeLimitsRead.byDir[dir]
	if state == nil {
		state = &claudeLimitsState{}
		claudeLimitsRead.byDir[dir] = state
	}
	claudeLimitsRead.mu.Unlock()
	state.mu.Lock()
	defer state.mu.Unlock()
	now := time.Now()
	if now.Before(state.until) {
		return AccountLimits{}, &limitsHTTPError{Status: http.StatusTooManyRequests, RetryAfter: state.until.Sub(now)}
	}
	if !state.at.IsZero() && now.Sub(state.at) < claudeLimitsEvery {
		return state.limits, nil
	}
	limits, err := read()
	var httpErr *limitsHTTPError
	switch {
	case err == nil:
		state.at, state.limits, state.until = time.Now(), limits, time.Time{}
	case errors.As(err, &httpErr) && httpErr.Status == http.StatusTooManyRequests:
		wait := min(max(httpErr.RetryAfter, time.Minute), 6*time.Hour)
		state.until = time.Now().Add(wait)
		httpErr.RetryAfter = wait
	}
	return limits, err
}

// claudeRefreshURL and claudeClientID are Claude Code's own OAuth refresh
// endpoint and public client (variables for the tests).
var (
	claudeRefreshURL = "https://platform.claude.com/v1/oauth/token"
	claudeClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeRefreshMu  sync.Mutex
)

// claudeAccessToken returns the access token of the folder's login.
//
// An expired token (or any, with force) is renewed with the refresh token
// only for a connection's own login folder (home not empty), which only 1loop's
// Claude Code processes use; the new tokens are saved in .credentials.json,
// its other fields untouched. The system login (home empty) is never
// renewed here: errClaudeExpired.
func claudeAccessToken(ctx context.Context, home string, force bool) (string, error) {
	claudeRefreshMu.Lock()
	defer claudeRefreshMu.Unlock()
	path := filepath.Join(claudeDataDir(home), ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		return "", err
	}
	var auth map[string]json.RawMessage
	if err := json.Unmarshal(file["claudeAiOauth"], &auth); err != nil {
		return "", errClaudeSignedOut
	}
	var access, refresh string
	var expires int64
	_ = json.Unmarshal(auth["accessToken"], &access)
	_ = json.Unmarshal(auth["refreshToken"], &refresh)
	_ = json.Unmarshal(auth["expiresAt"], &expires)
	if access != "" && !force && expires > time.Now().Add(30*time.Second).UnixMilli() {
		return access, nil
	}
	if home == "" {
		if access == "" && refresh == "" {
			return "", errClaudeSignedOut
		}
		return "", errClaudeExpired
	}
	if refresh == "" {
		return "", errClaudeSignedOut
	}
	// The refresh token is spent by the refresh: make sure the new tokens
	// can be saved before asking for them.
	tmp := path + ".1loop.tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	defer out.Close()
	body, _ := json.Marshal(map[string]string{"grant_type": "refresh_token", "refresh_token": refresh, "client_id": claudeClientID})
	req, err := http.NewRequestWithContext(ctx, "POST", claudeRefreshURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		return "", errClaudeSignedOut // the refresh token is revoked or already used
	}
	if resp.StatusCode != http.StatusOK {
		return "", &limitsHTTPError{Status: resp.StatusCode, RetryAfter: retryAfter(resp.Header, time.Now())}
	}
	var reply struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&reply); err != nil {
		return "", err
	}
	if reply.Access == "" {
		return "", io.ErrUnexpectedEOF
	}
	set := func(key string, value any) { auth[key], _ = json.Marshal(value) }
	set("accessToken", reply.Access)
	if reply.Refresh != "" {
		set("refreshToken", reply.Refresh)
	}
	set("expiresAt", time.Now().Add(time.Duration(reply.Expires)*time.Second).UnixMilli())
	file["claudeAiOauth"], _ = json.Marshal(auth)
	saved, _ := json.MarshalIndent(file, "", "  ")
	if _, err := out.Write(saved); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return reply.Access, nil
}
