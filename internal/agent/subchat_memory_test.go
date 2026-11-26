// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"strings"
	"testing"

	"1loop/internal/platform"
)

func TestDefaultMaxSubchats(t *testing.T) {
	const giB = uint64(1) << 30
	const miB = uint64(1) << 20
	cases := []struct {
		total uint64
		want  int
	}{
		{0, MaxRunningSubchats},
		{2 * giB, 1},
		{2970 * miB, 2}, // 3 GB VM (~2.9 GiB MemTotal)
		{3*giB + 400*miB, 2},
		{8 * giB, 3},
		{16 * giB, 4},
	}
	for _, tc := range cases {
		if got := DefaultMaxSubchats(tc.total); got != tc.want {
			t.Errorf("DefaultMaxSubchats(%d) = %d, want %d", tc.total, got, tc.want)
		}
	}
}

func TestSubchatMemoryCheck(t *testing.T) {
	const giB = uint64(1) << 30
	const miB = uint64(1) << 20
	healthy := platform.MemoryStatus{TotalBytes: 8 * giB, AvailableBytes: 4 * giB, PressureSome10: 0, PressureFull10: 0}

	if ok, reason := SubchatMemoryCheck(healthy, 0, 2); !ok || reason != "" {
		t.Fatalf("healthy solo: ok=%v reason=%q", ok, reason)
	}
	if ok, reason := SubchatMemoryCheck(healthy, 2, 2); ok || !strings.Contains(reason, "2 of 2") {
		t.Fatalf("at limit: ok=%v reason=%q", ok, reason)
	}
	lowParallel := platform.MemoryStatus{TotalBytes: 4 * giB, AvailableBytes: 500 * miB, PressureSome10: 0, PressureFull10: 0}
	if ok, reason := SubchatMemoryCheck(lowParallel, 1, 2); ok || !strings.Contains(reason, "Available memory is low") {
		t.Fatalf("low parallel mem: ok=%v reason=%q", ok, reason)
	}
	// Same 500 MiB is fine when no subchat is running yet (> 384 MiB).
	if ok, reason := SubchatMemoryCheck(lowParallel, 0, 2); !ok || reason != "" {
		t.Fatalf("solo 500 MiB: ok=%v reason=%q", ok, reason)
	}
	highPSI := platform.MemoryStatus{TotalBytes: 4 * giB, AvailableBytes: 2 * giB, PressureSome10: 30, PressureFull10: 1}
	if ok, reason := SubchatMemoryCheck(highPSI, 1, 2); ok || !strings.Contains(reason, "Memory pressure is high") {
		t.Fatalf("high PSI parallel: ok=%v reason=%q", ok, reason)
	}
	veryLowSolo := platform.MemoryStatus{TotalBytes: 1 * giB, AvailableBytes: 200 * miB, PressureSome10: 0, PressureFull10: 0}
	if ok, reason := SubchatMemoryCheck(veryLowSolo, 0, 1); ok || !strings.Contains(reason, "too small") {
		t.Fatalf("very low solo: ok=%v reason=%q", ok, reason)
	}
	veryHighPSI := platform.MemoryStatus{TotalBytes: 2 * giB, AvailableBytes: 1 * giB, PressureSome10: 40, PressureFull10: 15}
	if ok, reason := SubchatMemoryCheck(veryHighPSI, 0, 1); ok || !strings.Contains(reason, "too small") {
		t.Fatalf("very high PSI solo: ok=%v reason=%q", ok, reason)
	}
}

func TestSubchatToolQueuedOutput(t *testing.T) {
	access := &SubchatAccess{
		List: func(context.Context) []SubchatInfo { return nil },
		Start: func(_ context.Context, req SubchatRequest) (SubchatInfo, error) {
			return SubchatInfo{
				ID:     "q1",
				Title:  req.Title,
				State:  SubchatQueued,
				Folder: "/p",
				Note:   "1 of 1 subchats are already working; queued until one finishes.",
			}, nil
		},
	}
	out, err, _ := runSubchat(t, access, true, true, map[string]any{"action": "start", "title": "Build", "brief": "Run tests", "isolation": "shared"})
	if err != nil {
		t.Fatalf("queued start err = %v", err)
	}
	if !strings.Contains(out, "Queued subchat q1 “Build”") || !strings.Contains(out, "1 of 1 subchats") {
		t.Fatalf("queued output = %q", out)
	}
}
