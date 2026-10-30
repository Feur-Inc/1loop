package agent

// Network failures, as Codex handles them: a request that fails to connect
// or gets a temporary error (408, 429, 5xx) is sent again up to
// requestRetries times, waiting longer each time or as long as Retry-After
// says; a stream that stays silent for streamIdleTimeout is cut; and an
// answer whose stream breaks before anything was shown is asked again up
// to streamRetries times. Once text is on screen it is not asked again:
// the user would read it twice (Retry is there for that).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	requestRetries = 4
	streamRetries  = 5
	maxRetryAfter  = 60 * time.Second // a longer wait is a quota: say so instead of waiting
)

// streamIdleTimeout cuts a stream with no byte for that long (Codex: 300 s).
var streamIdleTimeout = 300 * time.Second

// retryBase is the first wait; each retry doubles it (tests shorten it).
var retryBase = 500 * time.Millisecond

var errStreamIdle = errors.New("the model's stream sent nothing for too long")

// requestFailed is a request that kept failing after its retries: the
// stream retries must not start them all over again.
type requestFailed struct{ err error }

func (failure requestFailed) Error() string { return failure.err.Error() }
func (failure requestFailed) Unwrap() error { return failure.err }

// RateLimitError preserves the provider's reset time for an interrupted run.
// A zero time means the provider did not say when retrying will be possible.
type RateLimitError struct {
	Cause error
	Reset time.Time
}

func (failure *RateLimitError) Error() string { return failure.Cause.Error() }
func (failure *RateLimitError) Unwrap() error { return failure.Cause }

// rateLimitFailure tells a rate limit (try again later) from a refused
// payment (credit or quota used up: waiting changes nothing). The reset
// time comes from what each provider sends:
//   - a ChatGPT plan (Codex's backend): usage_limit_reached with resets_at
//     (Unix seconds) or resets_in_seconds;
//   - OpenAI's API: x-ratelimit-reset-requests/-tokens ("1s", "6m0s");
//   - OpenRouter: X-RateLimit-Reset (Unix milliseconds) or Retry-After;
//   - any of them: Retry-After, or "try again in 1.2s" in the message.
func rateLimitFailure(status int, text string, header http.Header, cause error) error {
	if status != http.StatusTooManyRequests || paymentRefused(text) {
		return cause
	}
	var body struct {
		Error struct {
			ResetsAt          int64 `json:"resets_at"`
			ResetsInSeconds   int64 `json:"resets_in_seconds"`
			RetryAfterSeconds int64 `json:"retry_after_seconds"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(text), &body)
	now := time.Now()
	var reset time.Time
	switch {
	case body.Error.ResetsAt > 0:
		reset = time.Unix(body.Error.ResetsAt, 0)
	case body.Error.ResetsInSeconds > 0:
		reset = now.Add(time.Duration(body.Error.ResetsInSeconds) * time.Second)
	case body.Error.RetryAfterSeconds > 0:
		reset = now.Add(time.Duration(body.Error.RetryAfterSeconds) * time.Second)
	}
	if reset.IsZero() {
		if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
				reset = now.Add(time.Duration(seconds) * time.Second)
			} else if at, err := http.ParseTime(value); err == nil {
				reset = at
			}
		}
	}
	if reset.IsZero() {
		if value, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil && value > 0 {
			reset = unixAuto(value)
		}
	}
	if reset.IsZero() {
		for _, name := range []string{"X-Ratelimit-Reset-Requests", "X-Ratelimit-Reset-Tokens"} {
			if wait, err := time.ParseDuration(strings.TrimSpace(header.Get(name))); err == nil && wait > 0 && now.Add(wait).After(reset) {
				reset = now.Add(wait)
			}
		}
	}
	if reset.IsZero() {
		reset = tryAgainIn(text)
	}
	return &RateLimitError{Cause: cause, Reset: reset}
}

// paymentRefused is a 429 that waiting does not end: OpenAI's quota and
// billing limits, a plan without access to the model. It reads the error's
// code and type only: a ChatGPT plan's usage-limit message may mention
// credits, and that limit does reset.
func paymentRefused(text string) bool {
	var body struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(text), &body) != nil {
		return strings.Contains(text, "insufficient_quota")
	}
	if body.Error.Type == "usage_limit_reached" {
		return false
	}
	for _, value := range []string{body.Error.Type, body.Error.Code} {
		switch value {
		case "insufficient_quota", "billing_hard_limit_reached", "billing_not_active", "usage_not_included", "credit_balance_exhausted":
			return true
		}
	}
	return false
}

// unixAuto reads a Unix time in seconds or in milliseconds.
func unixAuto(value int64) time.Time {
	if value > 100000000000 {
		return time.UnixMilli(value)
	}
	return time.Unix(value, 0)
}

var tryAgain = regexp.MustCompile(`(?i)try again in ([0-9.]+)\s*(ms|s|m|h)`)

// tryAgainIn reads "Please try again in 1.2s" from a message.
func tryAgainIn(text string) time.Time {
	match := tryAgain.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}
	}
	wait, err := time.ParseDuration(match[1] + match[2])
	if err != nil || wait <= 0 {
		return time.Time{}
	}
	return time.Now().Add(wait)
}

// streamLimit is a limit reported inside a response stream (a ChatGPT plan
// sends rate_limit_exceeded or usage_limit_reached there too).
func streamLimit(code, message string, cause error) error {
	switch code {
	case "rate_limit_exceeded", "usage_limit_reached":
		return &RateLimitError{Cause: cause, Reset: tryAgainIn(message)}
	}
	return cause
}

type retryNoticeKey struct{}

// noticeRetry tells the interface a retry is coming ("Reconnecting…").
func noticeRetry(ctx context.Context, text string) {
	if notice, ok := ctx.Value(retryNoticeKey{}).(func(string)); ok {
		notice(text)
	}
}

// retryWait is how long to wait before retry n (0-based): Retry-After when
// the server gave one, else a doubling wait with some jitter.
func retryWait(n int, header http.Header) time.Duration {
	if header != nil {
		if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil {
				return time.Duration(seconds) * time.Second
			}
			if at, err := http.ParseTime(value); err == nil {
				return time.Until(at)
			}
		}
	}
	wait := retryBase << n
	if wait > 10*time.Second {
		wait = 10 * time.Second
	}
	return wait + time.Duration(rand.Int64N(int64(wait)/5+1))
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryableStatus are the answers worth asking again.
func retryableStatus(status int, body string) bool {
	switch status {
	case 408, 500, 502, 503, 504, 520, 521, 522, 523, 524:
		return true
	case 429:
		// A used-up plan is not a passing overload.
		lower := strings.ToLower(body)
		return !strings.Contains(lower, "usage_limit") && !strings.Contains(lower, "insufficient_quota")
	}
	return false
}

// networkFailure is a connection or stream that broke, as opposed to an
// answer from the model or the provider.
func networkFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.Is(err, errStreamIdle) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) || errors.As(err, &netErr) {
		return true
	}
	text := err.Error()
	for _, sign := range []string{"stream was interrupted", "connection reset", "unexpected EOF", "Incomplete OpenAI response", "http2: stream closed", "server sent GOAWAY", "TLS handshake timeout"} {
		if strings.Contains(text, sign) {
			return true
		}
	}
	return false
}

// withStreamRetries runs one answer, and asks again when its stream broke
// before anything reached the screen.
func withStreamRetries(ctx context.Context, emit emitter, attempt func(context.Context, emitter) (Message, error)) (Message, error) {
	ctx = context.WithValue(ctx, retryNoticeKey{}, func(text string) { emit("notice", text) })
	for n := 0; ; n++ {
		shown := false
		watch := func(kind string, value any) {
			if kind == "delta" || kind == "note" {
				shown = true
			}
			emit(kind, value)
		}
		out, err := attempt(ctx, watch)
		var exhausted requestFailed
		if err == nil || shown || n >= streamRetries || ctx.Err() != nil || errors.As(err, &exhausted) || !networkFailure(err) {
			return out, err
		}
		emit("notice", fmt.Sprintf("Connection lost, reconnecting (%d/%d)…", n+1, streamRetries))
		if sleepErr := sleepContext(ctx, retryWait(n, nil)); sleepErr != nil {
			return out, err
		}
	}
}

// send makes one request, retrying a failed connection or a temporary
// error; build gives a fresh request each time (its body is read once).
func send(ctx context.Context, client *http.Client, build func() (*http.Request, error)) (*http.Response, error) {
	for n := 0; ; n++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		response, err := client.Do(req)
		var header http.Header
		failure := err
		if err == nil {
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return response, nil
			}
			text, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
			response.Body.Close()
			failure = rateLimitFailure(response.StatusCode, string(text), response.Header, fmt.Errorf("HTTP %d · %s", response.StatusCode, text))
			header = response.Header
			if !retryableStatus(response.StatusCode, string(text)) {
				return nil, requestFailed{failure}
			}
		} else if ctx.Err() != nil || !networkFailure(err) {
			return nil, err
		}
		wait := retryWait(n, header)
		if n >= requestRetries || wait > maxRetryAfter {
			return nil, requestFailed{failure}
		}
		noticeRetry(ctx, fmt.Sprintf("Reconnecting (%d/%d)…", n+1, requestRetries))
		if err := sleepContext(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// idleBody cuts a response body that stays silent for streamIdleTimeout.
type idleBody struct {
	body  io.ReadCloser
	timer *time.Timer
	mu    sync.Mutex
	idle  bool
}

func watchIdle(body io.ReadCloser) io.ReadCloser {
	watched := &idleBody{body: body}
	watched.timer = time.AfterFunc(streamIdleTimeout, func() {
		watched.mu.Lock()
		watched.idle = true
		watched.mu.Unlock()
		body.Close()
	})
	return watched
}

func (watched *idleBody) Read(p []byte) (int, error) {
	n, err := watched.body.Read(p)
	if n > 0 {
		watched.timer.Reset(streamIdleTimeout)
	}
	if err != nil {
		watched.mu.Lock()
		idle := watched.idle
		watched.mu.Unlock()
		if idle {
			return n, errStreamIdle
		}
	}
	return n, err
}

func (watched *idleBody) Close() error {
	watched.timer.Stop()
	return watched.body.Close()
}
