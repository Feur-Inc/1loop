// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// The sidebar is built again at every change: rows keep their place by key,
// so a row that moved glides from where it was and a new one drops in.
func TestGlideLayout(t *testing.T) {
	test.NewTempApp(t)
	animationsOff = true // every glide lands at once: only where it started is checked
	defer func() { animationsOff = false }()
	glide := newGlideLayout()
	list := container.New(glide)
	list.Resize(fyne.NewSize(200, 400))
	render := func(keys ...string) map[string]*tapRow {
		rows := map[string]*tapRow{}
		list.RemoveAll()
		glide.begin()
		for _, key := range keys {
			row := newRow(key, nil, styleNav, func() {})
			row.selected = key == "b"
			rows[key] = row
			glide.key(row, key)
			list.Add(row)
		}
		glide.end(list)
		return rows
	}
	render("a", "b", "c")
	height := glide.places["b"].target
	if glide.places["a"].offset != 0 || height <= 0 {
		t.Fatalf("first rendering moved: %+v", glide.places)
	}
	rows := render("new", "a", "b", "c")
	if offset := glide.places["a"].offset; offset != -height {
		t.Errorf("a pushed down starts %v above its new place, want %v", offset, -height)
	}
	if !glide.fresh[rows["new"]] || glide.places["new"].offset >= 0 {
		t.Errorf("a new row drops in: fresh %v, offset %v", glide.fresh[rows["new"]], glide.places["new"].offset)
	}
	if rows["b"].Position().Y != glide.places["b"].target || rows["new"].alpha != 1 {
		t.Errorf("motion off: b at %v (want %v), new row's alpha %v", rows["b"].Position().Y, glide.places["b"].target, rows["new"].alpha)
	}
	if !rows["b"].pill || glide.pill.Position().Y != glide.places["b"].target || !glide.pill.Visible() {
		t.Errorf("the selection's fill is not on b: pill %v at %v", rows["b"].pill, glide.pill.Position())
	}
	render("a", "c")
	if glide.pill.Visible() {
		t.Error("the selection's fill stays with no row selected")
	}
}
