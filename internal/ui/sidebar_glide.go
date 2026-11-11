// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// glideLayout stacks the sidebar's projects and chats like a VBox, one
// point apart. The sidebar is built again at every change, so each row
// carries a key (a chat's id, a project's path): a row whose key was
// already there glides from where it was drawn to its new place, a new
// one fades in as it drops into place, and the selection's fill slides
// from the row that had it to the one that has it now. Rows that leave
// simply go: those below close the gap.
type glideLayout struct {
	keys     map[fyne.CanvasObject]string // the rows being shown, as they were added
	places   map[string]glidePlace        // where each key's row was put last
	fresh    map[fyne.CanvasObject]bool   // rows that came in at the last change
	building bool                         // rows are being added: no layout before the last one
	changed  bool                         // a new set of rows waits for its first layout
	laidOut  bool                         // something was shown before: changes since animate
	progress float32                      // of the running glide, 1 when still
	anim     *fyne.Animation
	objects  []fyne.CanvasObject
	pill     *canvas.Rectangle // the selection's fill, behind the rows
	pillKey  string            // the selected row's key
	pillFrom rect              // where the pill was when the glide started
	pillTo   rect
	pillFade [2]float32 // the pill's opacity at the start and the end of the glide
}

// glidePlace is a row's place: target is where the layout puts it, and
// offset how far from it the glide started.
type glidePlace struct{ target, offset float32 }

func newGlideLayout() *glideLayout {
	pill := canvas.NewRectangle(colSelected)
	pill.CornerRadius = 6
	pill.Hide()
	return &glideLayout{keys: map[fyne.CanvasObject]string{}, places: map[string]glidePlace{}, progress: 1, pill: pill}
}

// begin starts a new set of rows; add each with its key, then end.
func (glide *glideLayout) begin() {
	glide.building = true
	clear(glide.keys)
}

func (glide *glideLayout) key(object fyne.CanvasObject, key string) { glide.keys[object] = key }

func (glide *glideLayout) end(list *fyne.Container) {
	glide.building, glide.changed = false, true
	list.Refresh() // lays the new rows out from where the old ones were
}

// at is where a place's row is drawn now, part way through a glide.
func (glide *glideLayout) at(place glidePlace) float32 {
	return place.target + place.offset*(1-glide.progress)
}

func (glide *glideLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.Size{}
	shown := 0
	for _, object := range objects {
		if !object.Visible() {
			continue
		}
		min := object.MinSize()
		size.Width = fyne.Max(size.Width, min.Width)
		size.Height += min.Height
		shown++
	}
	if shown > 1 {
		size.Height += float32(shown - 1)
	}
	return size
}

func (glide *glideLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if glide.building {
		return
	}
	if !glide.changed {
		glide.relayout(objects, size)
		return
	}
	glide.changed = false
	places := make(map[string]glidePlace, len(glide.keys))
	fresh := map[fyne.CanvasObject]bool{}
	moved := false
	y := float32(0)
	for _, object := range objects {
		if !object.Visible() {
			continue
		}
		height := object.MinSize().Height
		object.Resize(fyne.NewSize(size.Width, height))
		place := glidePlace{target: y}
		key, ok := glide.keys[object]
		if !ok {
			object.Move(fyne.NewPos(0, y))
		} else {
			if old, seen := glide.places[key]; seen {
				place.offset = glide.at(old) - y
			} else if glide.laidOut {
				fresh[object] = true
				place.offset = -6 // drops in from just above
			}
			places[key] = place
			moved = moved || place.offset != 0
		}
		y += height + 1
	}
	pillFrom, pillShown := glide.pillRect()
	glide.places, glide.fresh, glide.objects = places, fresh, objects
	glide.laidOut = glide.laidOut || len(places) > 0
	stopMotion(&glide.anim)
	glide.progress = 0
	// Where the selection will be once every row is in place. The same row
	// still selected and nothing moved: it stays where it is.
	pillTo, pillKey, selected := glide.selectedRect(objects)
	if !moved && pillKey == glide.pillKey {
		pillFrom = pillTo
	}
	glide.pillKey = pillKey
	glide.pillFrom, glide.pillTo = pillFrom, pillTo
	glide.pillFade = [2]float32{0, 0}
	if pillShown {
		glide.pillFade[0] = 1
	}
	if selected {
		glide.pillFade[1] = 1
		if !pillShown {
			glide.pillFrom = pillTo // a new selection fades in where it is
		}
	} else {
		glide.pillTo = pillFrom // a selection that went fades out where it was
	}
	if !moved && glide.pillFrom == glide.pillTo && glide.pillFade[0] == glide.pillFade[1] {
		glide.step(1)
		return
	}
	glide.step(0)
	glide.anim = motion(200*time.Millisecond, easeOutCubic, glide.step)
}

// relayout lays the same rows out again (a resize, a row grown): their
// targets move, and a glide under way goes on towards them.
func (glide *glideLayout) relayout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, object := range objects {
		if !object.Visible() {
			continue
		}
		height := object.MinSize().Height
		object.Resize(fyne.NewSize(size.Width, height))
		if key, ok := glide.keys[object]; ok {
			place := glide.places[key]
			place.target = y
			glide.places[key] = place
		} else {
			object.Move(fyne.NewPos(0, y))
		}
		y += height + 1
	}
	glide.objects = objects
	if pillTo, _, selected := glide.selectedRect(objects); selected {
		glide.pillTo = pillTo
		if glide.progress >= 1 {
			glide.pillFrom = pillTo
		}
	}
	glide.step(glide.progress)
}

// step draws the glide at progress p.
func (glide *glideLayout) step(p float32) {
	glide.progress = p
	for _, object := range glide.objects {
		place, placed := glide.places[glide.keys[object]]
		if !placed || !object.Visible() {
			continue // no key: Layout put it in place
		}
		object.Move(fyne.NewPos(0, glide.at(place)))
		if glide.fresh[object] {
			fadeRows(object, p)
		}
	}
	from, to := glide.pillFrom, glide.pillTo
	lerp := func(a, b float32) float32 { return a + (b-a)*p }
	opacity := lerp(glide.pillFade[0], glide.pillFade[1])
	if opacity <= 0 {
		glide.pill.Hide()
		return
	}
	glide.pill.FillColor = withAlpha(colSelected, opacity)
	glide.pill.Move(fyne.NewPos(lerp(from.pos.X, to.pos.X), lerp(from.pos.Y, to.pos.Y)))
	glide.pill.Resize(fyne.NewSize(lerp(from.size.X, to.size.X), lerp(from.size.Y, to.size.Y)))
	glide.pill.Show()
	glide.pill.Refresh()
}

// pillRect is where the selection's fill is drawn now.
func (glide *glideLayout) pillRect() (rect, bool) {
	if !glide.pill.Visible() {
		return rect{}, false
	}
	at, size := glide.pill.Position(), glide.pill.Size()
	return rect{at, fyne.NewPos(size.Width, size.Height)}, true
}

// selectedRect finds the selected row among the rows, in the list's
// coordinates at their target places, and gives it to the pill to draw.
func (glide *glideLayout) selectedRect(objects []fyne.CanvasObject) (rect, string, bool) {
	for _, object := range objects {
		key, ok := glide.keys[object]
		if !ok || !object.Visible() {
			continue
		}
		row := firstRow(object)
		if row == nil || !row.selected || row.style != styleNav {
			continue
		}
		if !row.pill {
			row.pill = true
			row.Refresh()
		}
		offset, _ := offsetIn(object, row)
		size := row.Size()
		return rect{fyne.NewPos(offset.X, glide.places[key].target+offset.Y), fyne.NewPos(size.Width, size.Height)}, key, true
	}
	return rect{}, "", false
}

// firstRow is the tapRow a sidebar entry is made of (with its ⋯ after it).
func firstRow(object fyne.CanvasObject) *tapRow {
	switch object := object.(type) {
	case *tapRow:
		return object
	case *fyne.Container:
		for _, child := range object.Objects {
			if row := firstRow(child); row != nil {
				return row
			}
		}
	}
	return nil
}

// fadeRows sets the opacity of the rows an entry is made of.
func fadeRows(object fyne.CanvasObject, alpha float32) {
	switch object := object.(type) {
	case *tapRow:
		if object.alpha != alpha {
			object.alpha = alpha
			object.Refresh()
		}
	case *fyne.Container:
		for _, child := range object.Objects {
			fadeRows(child, alpha)
		}
	}
}
