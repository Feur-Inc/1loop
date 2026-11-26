// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464): not part of upstream Fyne.

package desktop

import "fyne.io/fyne/v2"

// PointerEvent is where the pointer is over a window's canvas, whatever
// object lies under it.
type PointerEvent struct {
	Canvas   fyne.Canvas
	Position fyne.Position // in the canvas' coordinates
	Inside   bool          // false once it left the window: Position is where it left
	Button   MouseButton   // the buttons held
}

// OnPointer, when set, is told of every move of the pointer over a window,
// its leaving, and its buttons: for effects that follow it over the whole
// window (1loop's shader backgrounds). Called on Fyne's thread.
var OnPointer func(PointerEvent)
