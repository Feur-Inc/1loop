// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"fmt"

	"1loop/internal/platform"
)

// Memory thresholds for starting a subchat (#649).
const (
	// MinSubchatAvailableParallel is the minimum MemAvailable needed to start
	// another subchat while at least one subchat is already working.
	MinSubchatAvailableParallel uint64 = 768 << 20 // 768 MiB
	// MinSubchatAvailableSolo is the minimum MemAvailable needed to start a
	// subchat when none is currently working.
	MinSubchatAvailableSolo uint64 = 384 << 20 // 384 MiB

	// MaxMemoryPressureSome / Full are the /proc/pressure/memory avg10
	// thresholds (%) above which new subchats wait in the queue.
	MaxMemoryPressureSome     = 25.0
	MaxMemoryPressureFull     = 5.0
	MaxMemoryPressureFullSolo = 10.0

	// MaxSubchatsSettingLimit is the highest value allowed for the server's
	// max-subchats setting.
	MaxSubchatsSettingLimit = 8
)

// DefaultMaxSubchats chooses the default maximum concurrent subchats from the
// machine's total memory (#649). Unknown memory (0) falls back to MaxRunningSubchats.
func DefaultMaxSubchats(totalBytes uint64) int {
	if totalBytes == 0 {
		return MaxRunningSubchats
	}
	const miB = uint64(1) << 20
	switch {
	case totalBytes < 2560*miB: // < 2.5 GiB (1–2 GB machines)
		return 1
	case totalBytes < 5632*miB: // < 5.5 GiB (3–4 GB machines)
		return 2
	case totalBytes < 9728*miB: // < 9.5 GiB (6–8 GB machines)
		return 3
	default:
		return MaxRunningSubchats
	}
}

// SubchatMemoryCheck decides whether a subchat can start right now given the
// machine's memory status, how many subchats are currently working, and the
// configured concurrency limit. When ok is false, reason explains why the
// subchat is queued (and whether the machine may be too small).
func SubchatMemoryCheck(mem platform.MemoryStatus, running, limit int) (ok bool, reason string) {
	if limit > 0 && running >= limit {
		return false, fmt.Sprintf("%d of %d subchats are already working; queued until one finishes.", running, limit)
	}
	if running > 0 {
		if mem.AvailableBytes > 0 && mem.AvailableBytes < MinSubchatAvailableParallel {
			return false, fmt.Sprintf("Available memory is low (%s free); queued until another subchat finishes or memory is back.", formatMemoryBytes(mem.AvailableBytes))
		}
		if mem.PressureFull10 >= MaxMemoryPressureFull || mem.PressureSome10 >= MaxMemoryPressureSome {
			return false, fmt.Sprintf("Memory pressure is high (some %.0f%%, full %.0f%%); queued until another subchat finishes or pressure drops.", max(0, mem.PressureSome10), max(0, mem.PressureFull10))
		}
		return true, ""
	}
	// No subchat is working yet: only queue when memory is critically tight,
	// and tell the calling agent the machine may be too small.
	if mem.AvailableBytes > 0 && mem.AvailableBytes < MinSubchatAvailableSolo {
		if mem.TotalBytes > 0 {
			return false, fmt.Sprintf("Available memory is very low (%s free of %s); queued until memory is back. This machine may be too small for a subchat.", formatMemoryBytes(mem.AvailableBytes), formatMemoryBytes(mem.TotalBytes))
		}
		return false, fmt.Sprintf("Available memory is very low (%s free); queued until memory is back. This machine may be too small for a subchat.", formatMemoryBytes(mem.AvailableBytes))
	}
	if mem.PressureFull10 >= MaxMemoryPressureFullSolo {
		return false, fmt.Sprintf("Memory pressure is very high (full %.0f%%); queued until pressure drops. This machine may be too small for a subchat.", mem.PressureFull10)
	}
	return true, ""
}

func formatMemoryBytes(b uint64) string {
	const giB = 1 << 30
	const miB = 1 << 20
	if b >= giB {
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(giB))
	}
	return fmt.Sprintf("%d MiB", b/miB)
}
