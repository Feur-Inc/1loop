// Package mcptunnel is a client of OpenAI's Secure MCP Tunnel: it long-polls
// the control plane for the MCP requests ChatGPT sends to a tunnel, hands
// each one to an in-process MCP handler and posts the result back. Nothing
// listens on the network and no separate program runs (the protocol:
// https://github.com/openai/tunnel-client/blob/main/docs/protocol.md).
package mcptunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	baseURL         = "https://api.openai.com/v1/tunnels/"
	wireVersion     = "2026-08-25"
	clientName      = "1loop"
	pollTimeout     = 15 * time.Second
	maxConcurrent   = 10
	maxResponseBody = 10 << 20 // the control plane's message limit
)

// Handler answers one MCP JSON-RPC message (a request or a notification)
// with its HTTP-style status code; a notification gets a nil reply.
type Handler func(ctx context.Context, message json.RawMessage, headers http.Header) (reply json.RawMessage, replyHeaders http.Header, code int)

// Tunnel serves one tunnel until its context ends.
type Tunnel struct {
	ID, Key  string
	Version  string // 1loop's version, sent as diagnostics
	Instance string // stable per installation, sent as diagnostics
	Handle   Handler
	// Status reports the connection: nil once a poll succeeded, the error
	// while the control plane cannot be reached or refuses the key.
	Status func(error)
	Client *http.Client
}

// command is one polled item; unknown fields are ignored.
type command struct {
	RequestID       string          `json:"request_id"`
	ShardToken      string          `json:"shard_token"`
	CommandType     string          `json:"command_type"`
	Channel         string          `json:"channel"`
	ResponseTimeout json.RawMessage `json:"response_timeout"`
	Headers         http.Header     `json:"headers"`
	JSONRPC         json.RawMessage `json:"jsonrpc"`
}

// AuthError is the control plane refusing the key or the tunnel: it needs
// the user, not a retry loop.
type AuthError struct{ Code int }

func (e AuthError) Error() string {
	return fmt.Sprintf("The tunnel refused this key or tunnel ID (HTTP %d): check them in Settings", e.Code)
}

// Run polls until ctx ends. Network failures are retried with backoff; a
// refused key is reported through Status and retried slowly.
func (tunnel *Tunnel) Run(ctx context.Context) error {
	if tunnel.Client == nil {
		tunnel.Client = &http.Client{Timeout: pollTimeout + 30*time.Second}
	}
	slots := make(chan struct{}, maxConcurrent)
	var work sync.WaitGroup
	defer work.Wait()
	failures := 0
	for ctx.Err() == nil {
		commands, received, err := tunnel.poll(ctx)
		if tunnel.Status != nil && ctx.Err() == nil {
			tunnel.Status(err)
		}
		if err != nil {
			failures++
			delay := backoff(failures)
			var auth AuthError
			if errors.As(err, &auth) {
				delay = time.Minute
			}
			select {
			case <-ctx.Done():
			case <-time.After(delay):
			}
			continue
		}
		failures = 0
		for _, item := range commands {
			deadline, ok := commandDeadline(item.ResponseTimeout, received)
			if ok && !time.Now().Before(deadline) {
				continue // already expired: dropped without a reply
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			work.Add(1)
			go func(item command) {
				defer func() { <-slots; work.Done() }()
				operation := ctx
				if ok {
					var cancel context.CancelFunc
					operation, cancel = context.WithDeadline(ctx, deadline)
					defer cancel()
				}
				tunnel.process(operation, item)
			}(item)
		}
	}
	return ctx.Err()
}

func (tunnel *Tunnel) poll(ctx context.Context) ([]command, time.Time, error) {
	query := url.Values{"limit": {"25"}, "timeout_ms": {strconv.Itoa(int(pollTimeout / time.Millisecond))}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+url.PathEscape(tunnel.ID)+"/poll?"+query.Encode(), nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	tunnel.headers(request)
	response, err := tunnel.Client.Do(request)
	if err != nil {
		return nil, time.Time{}, err
	}
	received := time.Now() // before the body: the commands' deadlines start here
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNoContent:
		return nil, received, nil
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound:
		return nil, received, AuthError{response.StatusCode}
	case response.StatusCode != http.StatusOK:
		return nil, received, fmt.Errorf("Tunnel poll: HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Commands []command `json:"commands"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBody)).Decode(&envelope); err != nil {
		return nil, received, fmt.Errorf("Tunnel poll: %w", err)
	}
	return envelope.Commands, received, nil
}

func (tunnel *Tunnel) process(ctx context.Context, item command) {
	reply := map[string]any{"request_id": item.RequestID}
	if item.Channel != "" {
		reply["channel"] = item.Channel
	}
	switch item.CommandType {
	case "jsonrpc":
		answer, headers, code := tunnel.Handle(ctx, item.JSONRPC, item.Headers)
		if ctx.Err() != nil {
			return // past its deadline: never a late reply
		}
		reply["resp_code"] = code
		if headers != nil {
			reply["resp_headers"] = headers
		}
		if answer == nil {
			reply["resp_type"] = "notify_ack"
		} else {
			reply["resp_type"], reply["resp_json"] = "jsonrpc_response", answer
		}
	case "session_termination":
		reply["resp_type"], reply["resp_code"] = "session_termination_response", http.StatusNoContent
	default:
		return // an unknown kind is never treated as JSON-RPC
	}
	body, err := json.Marshal(reply)
	if err != nil {
		return
	}
	for attempt := 1; ctx.Err() == nil; attempt++ {
		code, err := tunnel.respond(ctx, item.ShardToken, body)
		if err == nil && code < 300 || code == http.StatusNotFound { // 404: already fulfilled
			return
		}
		if err == nil && code != 408 && code != 429 && code != 502 && code != 503 && code != 504 {
			return
		}
		if attempt == 5 {
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff(attempt)):
		}
	}
}

func (tunnel *Tunnel) respond(ctx context.Context, shard string, body []byte) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+url.PathEscape(tunnel.ID)+"/response", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	tunnel.headers(request)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Tunnel-Shard-Token", shard)
	response, err := tunnel.Client.Do(request)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	response.Body.Close()
	return response.StatusCode, nil
}

func (tunnel *Tunnel) headers(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+tunnel.Key)
	request.Header.Set("X-Tunnel-Client-Name", clientName)
	request.Header.Set("X-Tunnel-Client-Version", tunnel.Version)
	request.Header.Set("X-Tunnel-Client-Wire-Protocol-Version", wireVersion)
	if tunnel.Instance != "" {
		request.Header.Set("X-Tunnel-Client-Instance-Id", tunnel.Instance)
		request.Header.Set("X-Tunnel-MCP-Server-Info", `{"version":1,"channels":[{"name":"main"}]}`)
	}
}

// One integer and one unit, nothing else (the protocol's grammar); anything
// else keeps the legacy behavior: no deadline.
var timeoutGrammar = regexp.MustCompile(`^[0-9]+(ns|us|ms|s|m|h)$`)

func commandDeadline(raw json.RawMessage, received time.Time) (time.Time, bool) {
	var text string
	if json.Unmarshal(raw, &text) != nil || !timeoutGrammar.MatchString(text) {
		return time.Time{}, false
	}
	duration, err := time.ParseDuration(text)
	if err != nil {
		return time.Time{}, false
	}
	return received.Add(duration), true
}

// backoff grows to a minute, with jitter.
func backoff(attempt int) time.Duration {
	delay := time.Second << min(attempt, 6)
	delay = min(delay, time.Minute)
	return delay/2 + time.Duration(rand.Int64N(int64(delay/2)))
}
