// SPDX-License-Identifier: MPL-2.0

//go:build linux

package platform

import "testing"

func TestParseMeminfo(t *testing.T) {
	sample := `MemTotal:        3512340 kB
MemFree:          182400 kB
MemAvailable:    1048576 kB
Buffers:           42000 kB
`
	total, avail := parseMeminfo(sample)
	if total != 3512340*1024 {
		t.Fatalf("total = %d, want %d", total, 3512340*1024)
	}
	if avail != 1048576*1024 {
		t.Fatalf("available = %d, want %d", avail, 1048576*1024)
	}
	if total, avail := parseMeminfo(""); total != 0 || avail != 0 {
		t.Fatalf("empty = (%d, %d), want (0, 0)", total, avail)
	}
}

func TestParseMemoryPSI(t *testing.T) {
	sample := `some avg10=12.50 avg60=8.10 avg300=2.00 total=123456
full avg10=3.25 avg60=1.00 avg300=0.50 total=45678
`
	some10, full10 := parseMemoryPSI(sample)
	if some10 != 12.50 || full10 != 3.25 {
		t.Fatalf("psi = (%.2f, %.2f), want (12.50, 3.25)", some10, full10)
	}
	if some10, full10 := parseMemoryPSI(""); some10 != -1 || full10 != -1 {
		t.Fatalf("empty psi = (%.2f, %.2f), want (-1, -1)", some10, full10)
	}
}

func TestReadMemoryLive(t *testing.T) {
	status := ReadMemory()
	if status.TotalBytes == 0 {
		t.Fatalf("ReadMemory() returned TotalBytes = 0 on Linux")
	}
}
