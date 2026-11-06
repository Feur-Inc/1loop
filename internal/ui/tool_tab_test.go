// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

// An empty tab ID keeps meaning the same browser tab when a terminal comes
// on show, instead of the newest browser tab (#207).
func TestToolTab(t *testing.T) {
	tabs := []panelTab{{title: "B1"}, {title: "B2"}, {title: "T"}, {title: "B3"}}
	id := func(tab panelTab) string {
		if tab.title[0] == 'B' {
			return tab.title
		}
		return ""
	}
	for _, c := range []struct {
		active int
		last   string
		want   int
	}{
		{1, "B1", 1},   // the browser tab on show
		{2, "B2", 1},   // a terminal on show: the one used last
		{2, "", 3},     // never used: the newest
		{2, "gone", 3}, // used, then closed: the newest
		{-1, "B1", 0},
	} {
		if got := toolTab(tabs, c.active, c.last, id); got != c.want {
			t.Errorf("active %d, last %q: %d, want %d", c.active, c.last, got, c.want)
		}
	}
	if got := toolTab(tabs[2:3], 0, "", id); got != -1 {
		t.Errorf("no browser tab: %d", got)
	}
}
