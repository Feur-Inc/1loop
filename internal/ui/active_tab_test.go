// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

func TestActiveAfterClose(t *testing.T) {
	for _, c := range []struct{ active, closed, remaining, want int }{
		{1, 0, 2, 0},  // [A, B, C], B active, A closed: B stays active
		{1, 2, 2, 1},  // C closed: B stays
		{1, 1, 2, 1},  // B itself closed: C, its neighbour
		{2, 2, 2, 1},  // the last tab closed while active: the new last
		{0, 0, 0, -1}, // the only tab closed
		{0, 0, 2, 0},  // the first tab closed while active: the next one
	} {
		if got := activeAfterClose(c.active, c.closed, c.remaining); got != c.want {
			t.Errorf("active %d, closed %d, %d left: %d, want %d", c.active, c.closed, c.remaining, got, c.want)
		}
	}
}
