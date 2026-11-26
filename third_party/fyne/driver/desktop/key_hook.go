// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #577): not part of upstream Fyne.

package desktop

import "fyne.io/fyne/v2"

// OnKeyDown, when set, hears every key pressed in a window before the
// focused object or the canvas does, whatever has the focus. It returns
// true to keep the key from them (1loop: Escape cancels a drag under way).
// Called on Fyne's thread.
var OnKeyDown func(fyne.Canvas, *fyne.KeyEvent) bool
