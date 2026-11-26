// SPDX-License-Identifier: MPL-2.0

//go:build !linux

package platform

// ReadMemory reports unknown memory status on platforms without /proc.
func ReadMemory() MemoryStatus {
	return MemoryStatus{PressureSome10: -1, PressureFull10: -1}
}
