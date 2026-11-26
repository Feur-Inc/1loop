// SPDX-License-Identifier: MPL-2.0

package ui

// The jump to the end (#605): while the conversation is scrolled up, a
// small round down arrow sits just above the composer, at its right end.
// A click brings the conversation back to its end and follows the live
// answer again. At the end, it is hidden.

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

var iconArrowDown = lineIcon("arrow-down", `<path d="M8 3.2v9.4M3.9 8.5 8 12.6l4.1-4.1"/>`)

const (
	jumpEndSize = 28 // the disc
	jumpEndGap  = 6  // between the disc and the composer
	// jumpEndAway is how far from the end the view must be: closer, the
	// conversation's end is in view and the arrow would only flicker.
	jumpEndAway = 24
)

// newJumpEnd builds the floating arrow, hidden.
func (ui *desktopUI) newJumpEnd() *fyne.Container {
	// Discreet: the chat's own surface, a soft edge, a muted arrow.
	disc := canvas.NewCircle(colSurface)
	disc.StrokeColor, disc.StrokeWidth = colBorder, 1
	button := newIconButton(iconArrowDown, colSurface, colMuted, ui.jumpToEnd)
	button.iconPx, button.padPx = 14, 5
	jump := container.NewStack(disc, container.NewCenter(button))
	jump.Hide()
	return jump
}

// jumpToEnd brings the conversation to its end and follows it again.
func (ui *desktopUI) jumpToEnd() {
	if ui.scroll == nil {
		return
	}
	ui.scroll.rest() // what the wheel still owed goes: it would scroll back up
	ui.followTail = true
	ui.scroll.ScrollToBottom()
	ui.syncFooterFade()
	relayout(ui.transcript)
}

// syncJumpEnd shows the arrow while the reader is away from the end.
func (ui *desktopUI) syncJumpEnd() {
	if ui.jumpEnd == nil || ui.scroll == nil {
		return
	}
	bottom := fyne.Max(0, ui.scroll.Content.Size().Height-ui.scroll.Size().Height)
	show := ui.scroll.Visible() && !ui.followTail && bottom-ui.scroll.Offset.Y > jumpEndAway
	if show != ui.jumpEnd.Visible() {
		if show {
			ui.jumpEnd.Show()
		} else {
			ui.jumpEnd.Hide()
		}
	}
}
