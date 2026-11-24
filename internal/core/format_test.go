// SPDX-License-Identifier: MPL-2.0

package core

import "testing"

func TestCompactCount(t *testing.T) {
	for n, want := range map[int]string{0: "0", 950: "950", 1000: "1k", 12345: "12.3k", 200000: "200k", 999949: "999.9k", 999999: "1M", 4100000: "4.1M"} {
		if got := CompactCount(n); got != want {
			t.Errorf("%d: %s, want %s", n, got, want)
		}
	}
}

func TestFormat(t *testing.T) {
	if Plural(1, "file") != "1 file" || Plural(0, "file") != "0 files" {
		t.Error("plural")
	}
	for n, want := range map[int64]string{512: "512 B", 12 << 10: "12 KB", 3 << 19: "1.5 MB"} {
		if got := ByteSize(n); got != want {
			t.Errorf("%d: %s, want %s", n, got, want)
		}
	}
	if got := string(TrimRune([]byte("caf\xc3"))); got != "caf" {
		t.Errorf("cut rune: %q", got)
	}
}
