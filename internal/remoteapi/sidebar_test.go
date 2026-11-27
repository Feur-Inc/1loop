// SPDX-License-Identifier: MPL-2.0

package remoteapi

import (
	"slices"
	"testing"
)

// A pin goes at the end of the pinned ones, a move among the pinned ones
// reorders them, a move among siblings ranks them in their new order, and
// a message sent ranks first (#699).
func TestSidebarOrder(t *testing.T) {
	var order SidebarOrder
	for _, key := range []string{ChatKey("a"), ProjectKey("/p")} {
		if err := order.Apply(SidebarChange{Action: "pin", Key: key}, 1); err != nil {
			t.Fatal(err)
		}
	}
	order.Apply(SidebarChange{Action: "move", Key: ProjectKey("/p"), To: 0}, 2)
	if !slices.Equal(order.Pinned, []string{ProjectKey("/p"), ChatKey("a")}) {
		t.Fatalf("pinned = %v", order.Pinned)
	}
	siblings := []string{ChatKey("b"), ChatKey("c"), ChatKey("d")}
	if err := order.Apply(SidebarChange{Action: "move", Key: ChatKey("d"), Siblings: siblings, To: 0}, 100); err != nil {
		t.Fatal(err)
	}
	if order.Rank[ChatKey("d")] >= order.Rank[ChatKey("b")] || order.Rank[ChatKey("b")] >= order.Rank[ChatKey("c")] {
		t.Errorf("ranks = %v, want d, b, c", order.Rank)
	}
	order.Touch(500, ChatKey("c"))
	if order.Rank[ChatKey("c")] >= 0 {
		t.Errorf("a used chat ranks %d, want before every moved one", order.Rank[ChatKey("c")])
	}
	order.Apply(SidebarChange{Action: "unpin", Key: ChatKey("a")}, 3)
	if slices.Contains(order.Pinned, ChatKey("a")) {
		t.Errorf("still pinned: %v", order.Pinned)
	}
	if order.Apply(SidebarChange{Action: "move", Key: ChatKey("x"), Siblings: siblings}, 4) == nil {
		t.Error("a move of a key that is not among its siblings went through")
	}
}
