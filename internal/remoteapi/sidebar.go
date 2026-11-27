// SPDX-License-Identifier: MPL-2.0

package remoteapi

// The sidebar's order (#283), kept by the engine for its own projects and
// chats, so that every client lists them alike (#699): GET /api/state and
// the "chats" event carry it, POST /api/sidebar changes it. A project or
// chat sorts by when a message was last sent in it, the most recent first;
// moving one by hand gives it and its siblings ranks in their new order,
// which holds until a message is sent in one of them. Pinned ones sit above
// the rest, in the order the user gave them.
//
// Ranks: a use is a negative number (the later, the smaller), a move a
// positive one, and 0 is a project or chat never used or moved, which keeps
// the order the list had before.

import (
	"fmt"
	"slices"
)

// SidebarOrder is an engine's pinned projects and chats and their ranks,
// by ProjectKey and ChatKey.
type SidebarOrder struct {
	Pinned []string         `json:"pinned"`
	Rank   map[string]int64 `json:"rank"`
}

func ProjectKey(project string) string { return "project:" + project }
func ChatKey(id string) string         { return "chat:" + id }

// SidebarChange is POST /api/sidebar's body: pin or unpin key, or move it
// to index To among Siblings, the keys of its list in the sidebar's order
// (the pinned ones, for a pinned key).
type SidebarChange struct {
	Action   string   `json:"action"` // pin, unpin or move
	Key      string   `json:"key"`
	Siblings []string `json:"siblings,omitempty"`
	To       int      `json:"to"`
}

// Touch ranks keys first: a message was sent in them. now is in
// nanoseconds.
func (order *SidebarOrder) Touch(now int64, keys ...string) {
	if order.Rank == nil {
		order.Rank = map[string]int64{}
	}
	for _, key := range keys {
		order.Rank[key] = -now
	}
}

// Apply makes a change; now is in nanoseconds.
func (order *SidebarOrder) Apply(change SidebarChange, now int64) error {
	if change.Key == "" {
		return fmt.Errorf("Say which project or chat")
	}
	switch change.Action {
	case "pin":
		if !slices.Contains(order.Pinned, change.Key) {
			order.Pinned = append(order.Pinned, change.Key)
		}
	case "unpin":
		order.Pinned = slices.DeleteFunc(order.Pinned, func(key string) bool { return key == change.Key })
	case "move":
		if slices.Contains(order.Pinned, change.Key) {
			order.Pinned = ReorderKeys(order.Pinned, change.Key, change.To)
			return nil
		}
		if !slices.Contains(change.Siblings, change.Key) {
			return fmt.Errorf("Not among its siblings")
		}
		if order.Rank == nil {
			order.Rank = map[string]int64{}
		}
		PlaceRank(order.Rank, change.Siblings, change.Key, change.To, now)
	default:
		return fmt.Errorf("Unknown sidebar change")
	}
	return nil
}

// PlaceRank puts key at index to among siblings, in the sidebar's order: the
// siblings take ranks from base in their new order, so the sidebar shows it.
func PlaceRank(rank map[string]int64, siblings []string, key string, to int, base int64) {
	for k, sibling := range ReorderKeys(siblings, key, to) {
		rank[sibling] = base + int64(k)
	}
}

// ReorderKeys is list with key moved to index to. The list is unchanged when
// it does not hold key.
func ReorderKeys(list []string, key string, to int) []string {
	if !slices.Contains(list, key) {
		return list
	}
	rest := slices.DeleteFunc(slices.Clone(list), func(k string) bool { return k == key })
	to = min(max(to, 0), len(rest))
	return slices.Insert(rest, to, key)
}
