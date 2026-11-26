// SPDX-License-Identifier: MPL-2.0

package ui

// The side panel's tab strip, kept in order as tabs pile up (#596). Tabs
// are grouped by family (tasks, review, tool calls, terminals, browser,
// Git and files), a thin rule between families. When they outgrow the
// panel, arrows at both ends scroll the strip, as Firefox's do, and the
// list button shows every tab, the hidden ones included, by family. The
// tab shown is always scrolled into view.

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

// tabFamily is a tab's family: its rank in the strip and its name.
func tabFamily(tab panelTab) (int, string) {
	switch strings.TrimPrefix(tab.kind, "server:") {
	case "todo":
		return 0, "Tasks"
	case "review":
		return 1, "Review"
	case "tool":
		return 2, "Tool calls"
	case "terminal":
		return 3, "Terminals"
	case "browser", "chatgptweb":
		return 4, "Browser"
	case "changes", "files", "github":
		return 5, "Git and files"
	}
	if tab.web {
		return 4, "Browser"
	}
	return 6, "Other"
}

// stripOrder is the order the strip shows tabs in: by family, then as
// they were opened.
func stripOrder(tabs []panelTab) []int {
	order := make([]int, len(tabs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		ra, _ := tabFamily(tabs[a])
		rb, _ := tabFamily(tabs[b])
		return ra - rb
	})
	return order
}

// familyRule is the thin rule between two families of tabs.
func familyRule() fyne.CanvasObject {
	rule := canvas.NewRectangle(colBorder)
	rule.SetMinSize(fyne.NewSize(1, 18))
	return padded(0, 0, 2, 2, container.NewCenter(rule))
}

// addOrderedTabs adds the panel's tabs to the strip, by family.
func (panel *toolPanel) addOrderedTabs() {
	last := -1
	for _, i := range stripOrder(panel.tabs) {
		rank, _ := tabFamily(panel.tabs[i])
		if last >= 0 && rank != last {
			panel.strip.Add(familyRule())
		}
		last = rank
		view := panel.tabView(i, panel.tabs[i])
		panel.stripViews[i] = view
		panel.strip.Add(view)
	}
}

// nextTitle tells apart the tabs of a kind opened several times: the
// second terminal is "Terminal 2". The number follows the highest one
// open, so it never repeats while its tabs are.
func (panel *toolPanel) nextTitle(kind, title string) string {
	highest := 0
	for _, tab := range panel.tabs {
		if tab.kind != kind {
			continue
		}
		n := 1
		if rest, ok := strings.CutPrefix(tab.title, title+" "); ok {
			if _, err := fmt.Sscan(rest, &n); err != nil {
				n = 1
			}
		}
		highest = max(highest, n)
	}
	if highest == 0 {
		return title
	}
	return fmt.Sprintf("%s %d", title, highest+1)
}

// listTabs shows every tab of the panel by family, the one shown checked;
// the first tab of each family says the family's name.
func (panel *toolPanel) listTabs() {
	var actions []menuAction
	last := ""
	for _, i := range stripOrder(panel.tabs) {
		i := i
		tab := panel.tabs[i]
		_, family := tabFamily(tab)
		detail := ""
		if family != last {
			detail, last = family, family
		}
		actions = append(actions, menuAction{label: fitText(tabLabel(tab), 13, fyne.TextStyle{}, 300), detail: detail, icon: tabIcon(tab),
			checked: i == panel.active, run: func() { panel.choose(i); panel.unfold() }})
	}
	if len(actions) == 0 {
		actions = append(actions, menuAction{label: "No tabs open", run: func() {}})
	}
	showMenu(panel.ui.window, panel.stripList, actions)
}

// stripStep is how far an arrow scrolls the strip: most of what shows.
func (panel *toolPanel) stripStep() float32 { return fyne.Max(80, panel.strips.Size().Width*0.7) }

// scrollStrip moves the strip by delta points, eased.
func (panel *toolPanel) scrollStrip(delta float32) {
	scroll := panel.strips
	most := fyne.Max(0, scroll.Content.MinSize().Width-scroll.Size().Width)
	from := scroll.Offset.X
	to := fyne.Min(most, fyne.Max(0, from+delta))
	stopMotion(&panel.stripMotion)
	panel.stripMotion = motion(180*time.Millisecond, fyne.AnimationEaseOut, func(p float32) {
		scroll.Offset.X = from + (to-from)*p
		scroll.Refresh()
		panel.syncStripArrows()
	})
}

// revealActive scrolls the strip so the tab shown is in view.
func (panel *toolPanel) revealActive() {
	view := panel.stripViews[panel.active]
	if view == nil || panel.strips.Size().Width <= 0 {
		panel.syncStripArrows()
		return
	}
	scroll := panel.strips
	x, w, width := view.Position().X, view.Size().Width, scroll.Size().Width
	offset := scroll.Offset.X
	switch {
	case x < offset:
		offset = x - 8
	case x+w > offset+width:
		offset = x + w - width + 8
	}
	offset = fyne.Min(fyne.Max(0, scroll.Content.MinSize().Width-width), fyne.Max(0, offset))
	if offset != scroll.Offset.X {
		scroll.Offset.X = offset
		scroll.Refresh()
	}
	panel.syncStripArrows()
}

// syncStripArrows shows the arrows while the tabs outgrow the strip, each
// dimmed at its end.
func (panel *toolPanel) syncStripArrows() {
	if panel.stripLeft == nil || panel.strips == nil {
		return
	}
	scroll := panel.strips
	room := scroll.Size().Width
	if panel.stripLeft.Visible() {
		room += panel.stripLeft.Size().Width + panel.stripRight.Size().Width
	}
	content := scroll.Content.MinSize().Width
	over := content > room+1
	for _, arrow := range []*tapRow{panel.stripLeft, panel.stripRight} {
		if over != arrow.Visible() {
			if over {
				arrow.Show()
			} else {
				arrow.Hide()
			}
		}
	}
	if !over {
		if scroll.Offset.X != 0 {
			scroll.Offset.X = 0
			scroll.Refresh()
		}
		return
	}
	atStart, atEnd := scroll.Offset.X <= 1, scroll.Offset.X >= content-scroll.Size().Width-1
	if atStart != panel.stripLeft.disabled {
		panel.stripLeft.disabled = atStart
		panel.stripLeft.Refresh()
	}
	if atEnd != panel.stripRight.disabled {
		panel.stripRight.disabled = atEnd
		panel.stripRight.Refresh()
	}
}

// newStripArrow is one of the strip's arrows.
func newStripArrow(icon fyne.Resource, onTap func()) *tapRow {
	arrow := newIconButton(icon, color.NRGBA{}, color.NRGBA{}, onTap)
	arrow.iconPx, arrow.padPx = 15, 7
	arrow.Hide()
	return arrow
}

// stripFrame lays the strip's scroll out and keeps its arrows in step with
// its width.
type stripFrame struct{ panel *toolPanel }

func (frame stripFrame) MinSize(objects []fyne.CanvasObject) fyne.Size { return objects[0].MinSize() }

func (frame stripFrame) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.Position{})
	objects[0].Resize(size)
	fyne.Do(frame.panel.syncStripArrows) // after this layout: showing an arrow lays the header out again
}
