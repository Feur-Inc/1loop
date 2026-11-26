// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

func blockingSubchatModel(t *testing.T, gate <-chan struct{}) string {
	t.Helper()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "HOLD") {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(service.Close)
	return service.URL + "/v1"
}

func TestSubchatQueueByLimitAndDrain(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()

	const giB = uint64(1) << 30
	server.readMemory = func() platform.MemoryStatus {
		return platform.MemoryStatus{TotalBytes: 2 * giB, AvailableBytes: 1500 << 20, PressureSome10: 0, PressureFull10: 0}
	}
	gate := make(chan struct{})
	baseURL := blockingSubchatModel(t, gate)

	server.mu.Lock()
	parent := &serverChat{ID: "parent", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Config: agent.Config{Provider: "compatible", BaseURL: baseURL, Model: "mock", Workspace: server.root}}
	server.chats = []*serverChat{parent}
	server.mu.Unlock()

	// Default on 2 GiB machine is 1 subchat at a time.
	info1, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "First", Brief: "HOLD first", Context: "fresh", Isolation: "shared"})
	if err != nil || info1.State != agent.SubchatWorking {
		t.Fatalf("first start = %+v, err = %v", info1, err)
	}
	info2, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "Second", Brief: "second task", Context: "fresh", Isolation: "shared"})
	if err != nil || info2.State != agent.SubchatQueued || !strings.Contains(info2.Note, "1 of 1") {
		t.Fatalf("second start = %+v, err = %v", info2, err)
	}

	server.mu.Lock()
	sub2 := server.chatLocked(info2.ID)
	meta2 := server.metaLocked(sub2)
	var summaryQueued bool
	for _, s := range server.summariesLocked() {
		if s.ID == info2.ID {
			summaryQueued = s.Queued
		}
	}
	server.mu.Unlock()
	if !summaryQueued || !strings.Contains(meta2.Label, "Queued") {
		t.Fatalf("queued visibility: summaryQueued=%v metaLabel=%q", summaryQueued, meta2.Label)
	}

	// Release first subchat: second should drain and finish automatically.
	close(gate)
	waitFor(t, server, "both subchats to finish", func() bool {
		s1 := server.chatLocked(info1.ID)
		s2 := server.chatLocked(info2.ID)
		return s1.run == nil && s1.Subchat.Ended == agent.SubchatDone &&
			s2.run == nil && s2.QueuedSubchat == nil && s2.Subchat.Ended == agent.SubchatDone
	})
}

func TestSubchatQueueByMemoryRecovery(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()

	const giB = uint64(1) << 30
	const miB = uint64(1) << 20
	var memMu sync.Mutex
	mem := platform.MemoryStatus{TotalBytes: 1 * giB, AvailableBytes: 200 * miB, PressureSome10: 0, PressureFull10: 0}
	server.readMemory = func() platform.MemoryStatus {
		memMu.Lock()
		defer memMu.Unlock()
		return mem
	}
	server.subchatQueuePoll = 25 * time.Millisecond

	baseURL := blockingSubchatModel(t, make(chan struct{}))
	server.mu.Lock()
	parent := &serverChat{ID: "parent", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Config: agent.Config{Provider: "compatible", BaseURL: baseURL, Model: "mock", Workspace: server.root}}
	server.chats = []*serverChat{parent}
	server.mu.Unlock()

	info, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "Tight", Brief: "work", Context: "fresh", Isolation: "shared"})
	if err != nil || info.State != agent.SubchatQueued || !strings.Contains(info.Note, "too small") {
		t.Fatalf("low-mem start = %+v, err = %v", info, err)
	}

	// Memory returns -> poll timer drains the queue.
	memMu.Lock()
	mem.AvailableBytes = 700 * miB
	memMu.Unlock()

	waitFor(t, server, "queued subchat to run once memory returns", func() bool {
		sub := server.chatLocked(info.ID)
		return sub != nil && sub.QueuedSubchat == nil && sub.run == nil && sub.Subchat.Ended == agent.SubchatDone
	})
}

func TestSubchatQueueStopAndSettingAPI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := newHeadlessServer(ctx, t.TempDir(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()

	const giB = uint64(1) << 30
	server.readMemory = func() platform.MemoryStatus {
		return platform.MemoryStatus{TotalBytes: 2 * giB, AvailableBytes: 1500 << 20, PressureSome10: 0, PressureFull10: 0}
	}
	gate := make(chan struct{})
	defer close(gate)
	baseURL := blockingSubchatModel(t, gate)

	server.mu.Lock()
	parent := &serverChat{ID: "parent", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Config: agent.Config{Provider: "compatible", BaseURL: baseURL, Model: "mock", Workspace: server.root}}
	server.chats = []*serverChat{parent}
	access := server.subchatAccess(parent)
	server.mu.Unlock()

	// Invalid values are rejected.
	for _, body := range []string{`{"maxSubchats":-1}`, `{"maxSubchats":9}`} {
		rec := httptest.NewRecorder()
		server.setMaxSubchats(rec, httptest.NewRequest("POST", "/api/max-subchats", strings.NewReader(body)))
		if rec.Code != 400 {
			t.Fatalf("%s -> %d, want 400", body, rec.Code)
		}
	}

	// Start 1 running + 2 queued.
	if _, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "One", Brief: "HOLD one", Context: "fresh", Isolation: "shared"}); err != nil {
		t.Fatal(err)
	}
	q1, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "Q1", Brief: "q1", Context: "fresh", Isolation: "shared"})
	if err != nil || q1.State != agent.SubchatQueued {
		t.Fatalf("q1 = %+v, %v", q1, err)
	}
	q2, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "Q2", Brief: "q2", Context: "fresh", Isolation: "shared"})
	if err != nil || q2.State != agent.SubchatQueued {
		t.Fatalf("q2 = %+v, %v", q2, err)
	}

	// Stop q1 while queued.
	if err := access.Stop(ctx, q1.ID); err != nil {
		t.Fatalf("stop queued q1: %v", err)
	}
	server.mu.Lock()
	if s := server.chatLocked(q1.ID); s.QueuedSubchat != nil || s.Subchat.Ended != agent.SubchatStopped {
		t.Fatalf("stopped q1: queued=%v ended=%q", s.QueuedSubchat != nil, s.Subchat.Ended)
	}
	server.mu.Unlock()

	// Raise maxSubchats to 2 via HTTP API -> q2 should immediately drain and finish!
	rec := httptest.NewRecorder()
	server.setMaxSubchats(rec, httptest.NewRequest("POST", "/api/max-subchats", strings.NewReader(`{"maxSubchats":2}`)))
	if rec.Code != 200 {
		t.Fatalf("setMaxSubchats = %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		MaxSubchats        int `json:"maxSubchats"`
		DefaultMaxSubchats int `json:"defaultMaxSubchats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.MaxSubchats != 2 || resp.DefaultMaxSubchats != 1 {
		t.Fatalf("response = %s", rec.Body.String())
	}
	waitFor(t, server, "q2 to finish after raising limit", func() bool {
		s := server.chatLocked(q2.ID)
		return s != nil && s.QueuedSubchat == nil && s.run == nil && s.Subchat.Ended == agent.SubchatDone
	})
}

func TestSubchatQueueSurvivesRestart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	gate := make(chan struct{})
	close(gate)
	baseURL := blockingSubchatModel(t, gate)

	dir := t.TempDir()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	s1, err := newHeadlessServer(ctx, root, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	s1.mu.Lock()
	s1.readMemory = func() platform.MemoryStatus {
		return platform.MemoryStatus{TotalBytes: 2 << 30, AvailableBytes: 300 << 20}
	}
	s1.subchatQueuePoll = time.Hour
	parent := &serverChat{ID: "parent", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Config: agent.Config{Provider: "compatible", BaseURL: baseURL, Model: "mock", Workspace: root}}
	s1.chats = []*serverChat{parent}
	s1.mu.Unlock()

	subInfo, err := s1.startSubchat(ctx, parent, agent.SubchatRequest{Title: "Saved", Brief: "keep this brief", Context: "fresh", Isolation: "shared"})
	if err != nil || subInfo.State != agent.SubchatQueued {
		t.Fatalf("startSubchat = %+v, %v", subInfo, err)
	}
	s1.mu.Lock()
	if err := s1.saveLocked(); err != nil {
		t.Fatal(err)
	}
	s1.mu.Unlock()
	s1.close()
	cancel()

	// Restart from the same dir with plenty of memory: the queued subchat must
	// not be marked interrupted, must keep its brief, and must start and finish.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	s2, err := newHeadlessServer(ctx2, root, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.close()
	s2.mu.Lock()
	if len(s2.cutOffs) != 0 {
		t.Fatalf("queued subchat was wrongly marked cut off: %+v", s2.cutOffs)
	}
	sub := s2.chatLocked(subInfo.ID)
	if sub == nil || sub.QueuedSubchat == nil || !strings.Contains(sub.QueuedSubchat.Content, "keep this brief") {
		t.Fatalf("reloaded queued subchat lost its brief: %+v", sub)
	}
	if got := s2.metaLocked(sub).Label; got != "Queued · waiting for memory" {
		t.Fatalf("meta label = %q, want %q", got, "Queued · waiting for memory")
	}
	s2.readMemory = func() platform.MemoryStatus {
		return platform.MemoryStatus{TotalBytes: 8 << 30, AvailableBytes: 4 << 30}
	}
	s2.resumeCutOffLocked(s2.runLocked)
	s2.mu.Unlock()

	waitFor(t, s2, "reloaded queued subchat to finish with its brief", func() bool {
		c := s2.chatLocked(subInfo.ID)
		return c != nil && c.QueuedSubchat == nil && c.run == nil && c.Subchat.Ended == agent.SubchatDone && len(c.Items) >= 2
	})
}

func TestSubchatStopParentDoesNotStartQueuedSibling(t *testing.T) {
	started := make(chan string, 10)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, m := range body.Messages {
			if strings.Contains(m.Content, "brief-") {
				started <- m.Content
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()

	server, _, _ := testServer(t)
	ctx := context.Background()
	server.mu.Lock()
	server.readMemory = func() platform.MemoryStatus {
		return platform.MemoryStatus{TotalBytes: 2 << 30, AvailableBytes: 300 << 20}
	}
	server.subchatQueuePoll = time.Hour
	parent := &serverChat{ID: "parent", Title: "Main", Todo: &agent.TodoState{}, Cache: &agent.ContextCache{},
		Config: agent.Config{Provider: "compatible", BaseURL: model.URL + "/v1", Model: "mock", Workspace: server.root}}
	server.chats = []*serverChat{parent}
	server.mu.Unlock()

	q1, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "Q1", Brief: "brief-1", Context: "fresh", Isolation: "shared"})
	if err != nil || q1.State != agent.SubchatQueued {
		t.Fatalf("q1 = %+v, %v", q1, err)
	}
	q2, err := server.startSubchat(ctx, parent, agent.SubchatRequest{Title: "Q2", Brief: "brief-2", Context: "fresh", Isolation: "shared"})
	if err != nil || q2.State != agent.SubchatQueued {
		t.Fatalf("q2 = %+v, %v", q2, err)
	}

	// Now memory is available, and we stop the parent: neither q1 nor q2 should start.
	server.mu.Lock()
	server.readMemory = func() platform.MemoryStatus {
		return platform.MemoryStatus{TotalBytes: 8 << 30, AvailableBytes: 4 << 30}
	}
	server.stopTreeLocked(parent, 0)
	s1, s2 := server.chatLocked(q1.ID), server.chatLocked(q2.ID)
	if s1.QueuedSubchat != nil || s2.QueuedSubchat != nil || s1.Subchat.Ended != agent.SubchatStopped || s2.Subchat.Ended != agent.SubchatStopped {
		t.Fatalf("expected both queued siblings stopped: s1=%+v s2=%+v", s1.Subchat, s2.Subchat)
	}
	server.mu.Unlock()

	select {
	case msg := <-started:
		t.Fatalf("sibling subchat was started while stopping parent: %q", msg)
	case <-time.After(100 * time.Millisecond):
	}
}
