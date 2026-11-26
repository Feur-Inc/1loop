// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// The sidebar's order (#283). A project or chat sorts by when a message was
// last sent in it, the most recent first. Moving one by hand (Move up, Move
// down) swaps it with its neighbour, so the move holds until a message is
// sent in one of them. Pinned projects and chats sit above the rest, in the
// order the user gave them; a message never moves them.
//
// Ranks: a use is a negative number (the later, the smaller), a move a
// positive one, and 0 is a project or chat never used or moved, which keeps
// the order the sidebar had before.

// projectKey and chatKey are a project's and a chat's keys in the sidebar:
// in sideRank, in the pinned list and in the rows' menus.
func projectKey(target projectTarget, project string) string {
	return "project:" + groupKey(target, project)
}

func chatKey(server, id string) string {
	if server != "" { // a moved chat has its ID here and on the server
		return "chat:" + server + ":" + id
	}
	return "chat:" + id
}

// touchOrder ranks a project and a chat first: a message was sent in them.
func (ui *desktopUI) touchOrder(target projectTarget, project, chatID string) {
	if ui.sideRank == nil {
		ui.sideRank = map[string]int64{}
	}
	now := -time.Now().UnixNano()
	if project != "" {
		ui.sideRank[projectKey(target, project)] = now
	}
	if chatID != "" {
		server := ""
		if target.kind == targetServer {
			server = target.id
		}
		ui.sideRank[chatKey(server, chatID)] = now
	}
	ui.save()
}

// touchChatOrder ranks a local or SSH chat, and its project, first.
func (ui *desktopUI) touchChatOrder(chat *conversation) {
	target := localTarget
	if chat.Config.MachineID != "" {
		target = sshTarget(chat.Config.MachineID)
	}
	project := ""
	if !chat.Config.InScratch() {
		project = ui.projectOf(chat.Config.Workspace)
	}
	ui.touchOrder(target, project, chat.ID)
}

// sortByRank orders items as the sidebar lists them: by rank, the ones
// never used or moved after the rest, in the order they came in.
func sortByRank[T any](items []T, key func(T) string, rank map[string]int64) {
	slices.SortStableFunc(items, func(a, b T) int {
		return cmp.Compare(rank[key(a)], rank[key(b)])
	})
}

// moveBy moves key one place up (delta -1) or down (+1) among siblings,
// which are in the sidebar's order, and saves the new order.
func (ui *desktopUI) moveBy(siblings []string, key string, delta int) {
	if ui.sideRank == nil {
		ui.sideRank = map[string]int64{}
	}
	if !moveRank(ui.sideRank, siblings, key, delta, time.Now().UnixNano()) {
		return
	}
	ui.save()
	ui.renderChats()
}

// moveRank moves key one place in the direction delta among siblings, in the
// sidebar's order. It says whether it moved anything.
func moveRank(rank map[string]int64, siblings []string, key string, delta int, base int64) bool {
	i := slices.Index(siblings, key)
	j := i + delta
	if i < 0 || j < 0 || j >= len(siblings) {
		return false
	}
	placeRank(rank, siblings, key, j, base)
	return true
}

// placeRank puts key at index to among siblings, in the sidebar's order: the
// siblings take ranks from base in their new order, so the sidebar shows it.
func placeRank(rank map[string]int64, siblings []string, key string, to int, base int64) {
	for k, sibling := range reorderKeys(siblings, key, to) {
		rank[sibling] = base + int64(k)
	}
}

// reorderKeys is list with key moved to index to. The list is unchanged when
// it does not hold key.
func reorderKeys(list []string, key string, to int) []string {
	if !slices.Contains(list, key) {
		return list
	}
	rest := slices.DeleteFunc(slices.Clone(list), func(k string) bool { return k == key })
	to = min(max(to, 0), len(rest))
	return slices.Insert(rest, to, key)
}

// movePinned moves a pinned project or chat up or down the pinned section.
func (ui *desktopUI) movePinned(key string, delta int) {
	i := slices.Index(ui.pinned, key)
	j := i + delta
	if i < 0 || j < 0 || j >= len(ui.pinned) {
		return
	}
	ui.pinned[i], ui.pinned[j] = ui.pinned[j], ui.pinned[i]
	ui.save()
	ui.renderChats()
}

// setPinned pins a project or chat, at the end of the pinned section, or
// unpins it, back to its place among the rest.
func (ui *desktopUI) setPinned(key string, pinned bool) {
	if pinned && !slices.Contains(ui.pinned, key) {
		ui.pinned = append(ui.pinned, key)
	}
	if !pinned {
		ui.pinned = slices.DeleteFunc(ui.pinned, func(k string) bool { return k == key })
	}
	ui.save()
	ui.renderChats()
}

// isPinned says whether a project or chat is in the pinned section.
func (ui *desktopUI) isPinned(key string) bool {
	return slices.Contains(ui.pinned, key)
}

// orderActions are a project's or chat's menu entries for its place. A
// pinned one can be unpinned and moved within the pinned section; any other
// can be pinned and moved among siblings, the unpinned keys of its list in
// the sidebar's order.
func (ui *desktopUI) orderActions(key string, siblings []string) []menuAction {
	if ui.isPinned(key) {
		return ui.pinnedActions(key)
	}
	actions := []menuAction{{label: "Pin", icon: iconBookmark, run: func() { ui.setPinned(key, true) }}}
	i := slices.Index(siblings, key)
	if i > 0 {
		actions = append(actions, menuAction{label: "Move up", icon: iconChevronUp, run: func() { ui.moveBy(siblings, key, -1) }})
	}
	if i >= 0 && i < len(siblings)-1 {
		actions = append(actions, menuAction{label: "Move down", icon: iconChevron, run: func() { ui.moveBy(siblings, key, 1) }})
	}
	return actions
}

// pinnedActions are a pinned project's or chat's menu entries.
func (ui *desktopUI) pinnedActions(key string) []menuAction {
	actions := []menuAction{{label: "Unpin", icon: iconBookmark, run: func() { ui.setPinned(key, false) }}}
	i := slices.Index(ui.pinned, key)
	if i > 0 {
		actions = append(actions, menuAction{label: "Move up", icon: iconChevronUp, run: func() { ui.movePinned(key, -1) }})
	}
	if i < len(ui.pinned)-1 {
		actions = append(actions, menuAction{label: "Move down", icon: iconChevron, run: func() { ui.movePinned(key, 1) }})
	}
	return actions
}

// projectSiblings are the keys a project row's drag and move go among: the
// pinned section's when the project is pinned, else its target's unpinned ones.
func (ui *desktopUI) projectSiblings(group targetGroup, key string) []string {
	if ui.isPinned(key) {
		return slices.Clone(ui.pinned)
	}
	return ui.siblingProjects(group)
}

// siblingProjects are the keys of a target's projects the sidebar lists
// unpinned, in its order: the ones orderActions moves among.
func (ui *desktopUI) siblingProjects(group targetGroup) []string {
	var keys []string
	for _, project := range group.listed() {
		if key := projectKey(group.target, project); !ui.isPinned(key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// unpinnedChatKeys are the keys of a project's chats not pinned, in order.
func (ui *desktopUI) unpinnedChatKeys(chats []sideChat) []string {
	var keys []string
	for _, chat := range chats {
		if key := chatKey(chat.target, chat.id); !ui.isPinned(key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// isProjectKey says whether a sidebar key names a project rather than a chat.
func isProjectKey(key string) bool { return strings.HasPrefix(key, "project:") }
