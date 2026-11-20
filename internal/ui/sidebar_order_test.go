// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"slices"
	"testing"
)

func TestSortByRankPutsUsedFirst(t *testing.T) {
	rank := map[string]int64{"chat:a": -100, "chat:c": -300}
	chats := []string{"chat:b", "chat:a", "chat:c", "chat:d"}
	sortByRank(chats, func(key string) string { return key }, rank)
	want := []string{"chat:c", "chat:a", "chat:b", "chat:d"}
	if !slices.Equal(chats, want) {
		t.Fatalf("order %v, want %v", chats, want)
	}
}

func TestSortByRankKeepsUnusedInOrder(t *testing.T) {
	items := []string{"x", "y", "z"}
	sortByRank(items, func(key string) string { return key }, nil)
	if !slices.Equal(items, []string{"x", "y", "z"}) {
		t.Fatalf("unused items reordered: %v", items)
	}
}

func TestMoveRankSwapsNeighbours(t *testing.T) {
	rank := map[string]int64{"p:a": -300, "p:b": -200, "p:c": -100}
	siblings := []string{"p:a", "p:b", "p:c"}
	if !moveRank(rank, siblings, "p:b", -1, 1000) {
		t.Fatal("moving b up was refused")
	}
	sorted := slices.Clone(siblings)
	sortByRank(sorted, func(key string) string { return key }, rank)
	if want := []string{"p:b", "p:a", "p:c"}; !slices.Equal(sorted, want) {
		t.Fatalf("after moving b up: %v, want %v", sorted, want)
	}
}

func TestMoveRankKeepsNeverUsedOrderAndHolds(t *testing.T) {
	rank := map[string]int64{}
	siblings := []string{"p:a", "p:b", "p:c"}
	// c moves to the top, each time from the order the sidebar shows.
	for range 2 {
		moveRank(rank, siblings, "p:c", -1, 1000)
		siblings = []string{"p:a", "p:b", "p:c"}
		sortByRank(siblings, func(key string) string { return key }, rank)
	}
	if want := []string{"p:c", "p:a", "p:b"}; !slices.Equal(siblings, want) {
		t.Fatalf("after two moves up: %v, want %v", siblings, want)
	}
	sorted := slices.Clone(siblings)
	// A message sent in b puts it above all moved ones.
	rank["p:b"] = -5
	sorted = slices.Clone(siblings)
	sortByRank(sorted, func(key string) string { return key }, rank)
	if sorted[0] != "p:b" {
		t.Fatalf("a used chat should go first, got %v", sorted)
	}
}

func TestMoveRankRefusesEdges(t *testing.T) {
	rank := map[string]int64{}
	siblings := []string{"p:a", "p:b"}
	if moveRank(rank, siblings, "p:a", -1, 1000) {
		t.Fatal("the first one moved up")
	}
	if moveRank(rank, siblings, "p:b", 1, 1000) {
		t.Fatal("the last one moved down")
	}
	if moveRank(rank, siblings, "p:missing", 1, 1000) {
		t.Fatal("a key not listed moved")
	}
}

func TestReorderKeysMovesKeyToIndex(t *testing.T) {
	list := []string{"p:a", "p:b", "p:c", "p:d"}
	if got := reorderKeys(list, "p:a", 2); !slices.Equal(got, []string{"p:b", "p:c", "p:a", "p:d"}) {
		t.Fatalf("a to 2: %v", got)
	}
	if got := reorderKeys(list, "p:d", 0); !slices.Equal(got, []string{"p:d", "p:a", "p:b", "p:c"}) {
		t.Fatalf("d to 0: %v", got)
	}
	if got := reorderKeys(list, "p:b", 99); !slices.Equal(got, []string{"p:a", "p:c", "p:d", "p:b"}) {
		t.Fatalf("b past the end: %v", got)
	}
	if got := reorderKeys(list, "p:missing", 0); !slices.Equal(got, list) {
		t.Fatalf("a key not listed changed the list: %v", got)
	}
	if !slices.Equal(list, []string{"p:a", "p:b", "p:c", "p:d"}) {
		t.Fatalf("the input list was changed: %v", list)
	}
}

func TestPlaceRankFollowsDrop(t *testing.T) {
	rank := map[string]int64{"p:a": -300, "p:b": -200, "p:c": -100}
	siblings := []string{"p:a", "p:b", "p:c"}
	placeRank(rank, siblings, "p:c", 0, 1000)
	sorted := slices.Clone(siblings)
	sortByRank(sorted, func(key string) string { return key }, rank)
	if want := []string{"p:c", "p:a", "p:b"}; !slices.Equal(sorted, want) {
		t.Fatalf("after dropping c first: %v, want %v", sorted, want)
	}
}
