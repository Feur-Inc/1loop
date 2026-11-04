// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

func TestSidebarWidthWithin(t *testing.T) {
	for _, c := range []struct{ width, total, want float32 }{
		{280, 1400, 280},
		{90, 1400, sidebarMinWidth},   // never narrower than the minimum
		{900, 1400, sidebarMaxWidth},  // nor wider than the maximum
		{500, 900, 900 - sidebarRoom}, // the conversation keeps its room
		{500, 600, sidebarMinWidth},   // a tiny window: the minimum wins
	} {
		if got := sidebarWidthWithin(c.width, c.total); got != c.want {
			t.Errorf("sidebarWidthWithin(%v, %v) = %v, want %v", c.width, c.total, got, c.want)
		}
	}
}
