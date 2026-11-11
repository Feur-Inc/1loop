// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"math"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
)

// viewZoom is the reader's own zoom and pan over a browser's picture: only
// what is shown here changes, never the page, its input or what the agent
// sees. While zoomed the view is read-only; the phone does the same
// (android ViewZoom.kt).
type viewZoom struct {
	scale float32 // 0 or 1: the agent's view, unzoomed
	x, y  float32 // where the zoomed picture's corner sits, in the screen's points
	url   string  // the page last shown: a navigation resets the view
}

const viewZoomMax = 5

func (zoom *viewZoom) active() bool { return zoom.scale > 1 }

func (zoom *viewZoom) factor() float32 { return max(1, zoom.scale) }

// zoomAt scales the view by factor, keeping the point under at still.
func (zoom *viewZoom) zoomAt(factor float32, at fyne.Position, size fyne.Size) {
	from := zoom.factor()
	to := min(viewZoomMax, max(1, from*factor))
	zoom.x = at.X - (at.X-zoom.x)*to/from
	zoom.y = at.Y - (at.Y-zoom.y)*to/from
	zoom.scale = to
	zoom.clamp(size)
}

// pan moves the zoomed picture by delta, never past its edges.
func (zoom *viewZoom) pan(delta fyne.Delta, size fyne.Size) {
	zoom.x += delta.DX
	zoom.y += delta.DY
	zoom.clamp(size)
}

func (zoom *viewZoom) clamp(size fyne.Size) {
	if !zoom.active() {
		zoom.scale, zoom.x, zoom.y = 1, 0, 0
		return
	}
	zoom.x = min(0, max(size.Width-size.Width*zoom.scale, zoom.x))
	zoom.y = min(0, max(size.Height-size.Height*zoom.scale, zoom.y))
}

func (zoom *viewZoom) reset() { zoom.scale, zoom.x, zoom.y = 1, 0, 0 }

// follow notes the page's address and resets the view when it changed: the
// view follows the agent again when it navigates.
func (zoom *viewZoom) follow(url string) bool {
	changed := zoom.url != "" && url != zoom.url && zoom.active()
	zoom.url = url
	if changed {
		zoom.reset()
	}
	return changed
}

// at maps a point of the unzoomed view to where it is shown.
func (zoom *viewZoom) at(pos fyne.Position) fyne.Position {
	scale := zoom.factor()
	return fyne.NewPos(zoom.x+pos.X*scale, zoom.y+pos.Y*scale)
}

func (zoom *viewZoom) sized(size fyne.Size) fyne.Size {
	return fyne.NewSize(size.Width*zoom.factor(), size.Height*zoom.factor())
}

func (zoom *viewZoom) label() string { return fmt.Sprintf("%.0f%%", zoom.factor()*100) }

// wheelFactor turns a Ctrl+scroll (or a pinch the system reports as one)
// into a zoom step. A notch of the wheel (25 on Linux and Windows, 10 on
// macOS) is about 16%; the driver's acceleration of fast scrolling is cut
// off, a trackpad's small steps stay proportional.
func wheelFactor(dy float32) float32 {
	return float32(math.Exp(float64(min(25, max(-25, dy))) * 0.006))
}

// zoomModifier: Ctrl (or Cmd) is held, from the screen's own tracking or
// the driver's, so a scroll zooms the view.
func zoomModifier(held bool) bool {
	if app := fyne.CurrentApp(); app != nil {
		if driver, ok := app.Driver().(desktop.Driver); ok {
			held = held || driver.CurrentKeyModifiers()&(fyne.KeyModifierControl|fyne.KeyModifierSuper) != 0
		}
	}
	return held
}

// newZoomBadge is the sign that the view is zoomed, shown over its corner,
// with the way back to the agent's view.
func newZoomBadge(reset func()) (*fyne.Container, *canvas.Text) {
	text := textLine("100%", 11.5, colText)
	text.TextStyle = fyne.TextStyle{Bold: true}
	icon := canvas.NewImageFromResource(tinted(iconSearch, colAmber))
	icon.SetMinSize(fyne.NewSquareSize(14))
	background := canvas.NewRectangle(colPopover)
	background.StrokeColor, background.StrokeWidth, background.CornerRadius = colPopBorder, 1, 15
	row := container.NewHBox(icon, text, spacer(2, 0), textLine("View only", 11.5, colMuted), newRow("Reset view", iconReload, styleChip, reset))
	badge := container.NewStack(background, padded(1, 1, 10, 4, row))
	badge.Hide()
	return badge, text
}

// placeZoomBadge shows the badge in the view's top right corner while zoomed.
func placeZoomBadge(badge *fyne.Container, text *canvas.Text, zoom *viewZoom, size fyne.Size) {
	if !zoom.active() {
		badge.Hide()
		return
	}
	if text.Text != zoom.label() {
		text.Text = zoom.label()
		text.Refresh()
	}
	min := badge.MinSize()
	badge.Resize(min)
	badge.Move(fyne.NewPos(max(0, size.Width-min.Width-10), 10))
	badge.Show()
}
