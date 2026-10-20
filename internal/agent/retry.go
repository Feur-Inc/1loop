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
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
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
			failure = fmt.Errorf("HTTP %d · %s", response.StatusCode, text)
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
