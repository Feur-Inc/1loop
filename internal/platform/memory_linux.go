// SPDX-License-Identifier: MPL-2.0

//go:build linux

package platform

import (
	"os"
	"strconv"
	"strings"
)

// ReadMemory reads /proc/meminfo and /proc/pressure/memory on Linux.
// Missing or unreadable files leave the corresponding fields zero / -1.
func ReadMemory() MemoryStatus {
	status := MemoryStatus{PressureSome10: -1, PressureFull10: -1}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		status.TotalBytes, status.AvailableBytes = parseMeminfo(string(data))
	}
	if data, err := os.ReadFile("/proc/pressure/memory"); err == nil {
		status.PressureSome10, status.PressureFull10 = parseMemoryPSI(string(data))
	}
	return status
}

func parseMeminfo(data string) (total, available uint64) {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var kb uint64
		var err error
		switch fields[0] {
		case "MemTotal:":
			if kb, err = strconv.ParseUint(fields[1], 10, 64); err == nil {
				total = kb * 1024
			}
		case "MemAvailable:":
			if kb, err = strconv.ParseUint(fields[1], 10, 64); err == nil {
				available = kb * 1024
			}
		}
	}
	return total, available
}

func parseMemoryPSI(data string) (some10, full10 float64) {
	some10, full10 = -1, -1
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var target *float64
		switch fields[0] {
		case "some":
			target = &some10
		case "full":
			target = &full10
		default:
			continue
		}
		for _, field := range fields[1:] {
			if value, ok := strings.CutPrefix(field, "avg10="); ok {
				if parsed, err := strconv.ParseFloat(value, 64); err == nil {
					*target = parsed
				}
				break
			}
		}
	}
	return some10, full10
}
