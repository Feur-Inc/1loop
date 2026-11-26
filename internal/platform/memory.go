// SPDX-License-Identifier: MPL-2.0

package platform

// MemoryStatus is a snapshot of the machine's memory and memory pressure
// (Linux /proc/meminfo and /proc/pressure/memory; zero / -1 elsewhere).
type MemoryStatus struct {
	TotalBytes     uint64  // MemTotal in bytes, 0 if unknown
	AvailableBytes uint64  // MemAvailable in bytes, 0 if unknown
	PressureSome10 float64 // /proc/pressure/memory "some avg10" (0..100), -1 if unavailable
	PressureFull10 float64 // /proc/pressure/memory "full avg10" (0..100), -1 if unavailable
}
