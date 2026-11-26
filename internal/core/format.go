// SPDX-License-Identifier: MPL-2.0

package core

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Plural is a count and its noun: "1 file", "3 files".
func Plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ByteSize is a size at a glance: 512 B, 12 KB, 1.5 MB.
func ByteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// CompactCount is a number at a glance: 950, 12.3k, 4.1M.
func CompactCount(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 999_950: // above, it rounds to 1000k
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e3), ".0") + "k"
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
}

// TrimRune drops a multi-byte character a limit cut in half at the end.
func TrimRune(data []byte) []byte {
	for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
		data = data[:len(data)-1]
	}
	return data
}
