// SPDX-License-Identifier: MPL-2.0

package ui

// What a server window asks of its server, for the developer log: the
// requests by route and the bytes received, every ten seconds while any
// came (#591).

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"1loop/internal/platform"
)

// remoteTraffic counts a client's requests and the bytes of their answers.
type remoteTraffic struct {
	mu       sync.Mutex
	requests map[string]int // by route, the chat's ID left out
	received int64
}

// route is a request's path as the counter groups it: /api/chats/<id>/panel
// is "/api/chats/*/panel", without the query.
func route(path string) string {
	path, _, _ = strings.Cut(path, "?")
	if rest, ok := strings.CutPrefix(path, "/api/chats/"); ok {
		_, after, _ := strings.Cut(rest, "/")
		if after == "" {
			return "/api/chats/*"
		}
		return "/api/chats/*/" + after
	}
	return path
}

func (traffic *remoteTraffic) request(path string) {
	if traffic == nil || platform.Dev == nil {
		return
	}
	traffic.mu.Lock()
	if traffic.requests == nil {
		traffic.requests = map[string]int{}
	}
	traffic.requests[route(path)]++
	traffic.mu.Unlock()
}

// body counts what is read of an answer.
func (traffic *remoteTraffic) body(body io.ReadCloser) io.ReadCloser {
	if traffic == nil || platform.Dev == nil {
		return body
	}
	return countedBody{body, traffic}
}

type countedBody struct {
	io.ReadCloser
	traffic *remoteTraffic
}

func (body countedBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	body.traffic.mu.Lock()
	body.traffic.received += int64(n)
	body.traffic.mu.Unlock()
	return n, err
}

// take returns the counts since the last call, and starts again.
func (traffic *remoteTraffic) take() (map[string]int, int64) {
	traffic.mu.Lock()
	defer traffic.mu.Unlock()
	requests, received := traffic.requests, traffic.received
	traffic.requests, traffic.received = nil, 0
	return requests, received
}

// summary is one line of the developer log for a period.
func trafficSummary(requests map[string]int, received int64, period time.Duration) string {
	total := 0
	routes := make([]string, 0, len(requests))
	for path, count := range requests {
		total += count
		routes = append(routes, path)
	}
	slices.SortFunc(routes, func(a, b string) int { return requests[b] - requests[a] })
	parts := make([]string, 0, len(routes))
	for _, path := range routes {
		parts = append(parts, fmt.Sprintf("%s %d", path, requests[path]))
	}
	seconds := period.Seconds()
	return fmt.Sprintf("%d requests (%.1f/s), %d KB received (%.1f KB/s): %s",
		total, float64(total)/seconds, received>>10, float64(received)/1024/seconds, strings.Join(parts, ", "))
}

// logTraffic writes the window's traffic to the developer log every ten
// seconds, until the server is left.
func (remote *remoteDesktop) logTraffic() {
	if platform.Dev == nil {
		return
	}
	const period = 10 * time.Second
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-remote.ctx.Done():
			return
		case <-ticker.C:
			if requests, received := remote.client.traffic.take(); len(requests) > 0 || received > 0 {
				platform.Devf("server", "traffic in %s: %s", period, trafficSummary(requests, received, period))
			}
		}
	}
}
