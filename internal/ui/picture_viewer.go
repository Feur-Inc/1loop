// SPDX-License-Identifier: MPL-2.0

package ui

// Attached pictures (#435): a thumbnail shown with its own shape, never
// larger than its box nor than the picture itself, and a viewer that opens
// it over the whole window, to zoom, move around, copy or save it.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/core"
)

// Thumbnail boxes: the composer's are 64 high, as wide as their shape asks
// up to 192; a sent message's fit in 240 × 160. Neither is ever enlarged.
const (
	composerPictureHeight = 64
	composerPictureWidth  = 192
	messagePictureWidth   = 240
	messagePictureHeight  = 160
)

// fitPictureBox is a w × h picture's size inside a maxW × maxH box, with its
// own aspect ratio, never larger than the picture.
func fitPictureBox(w, h int, maxW, maxH float32) fyne.Size {
	if w <= 0 || h <= 0 {
		return fyne.Size{}
	}
	scale := fyne.Min(1, fyne.Min(maxW/float32(w), maxH/float32(h)))
	// Clamped: rounding would otherwise leave it a hair past its box.
	return fyne.NewSize(fyne.Min(maxW, fyne.Max(1, float32(w)*scale)), fyne.Min(maxH, fyne.Max(1, float32(h)*scale)))
}

// ---------------------------------------------------------------------------
// pictureTile: one thumbnail, a click opens it, and in the composer a small
// ✕ on its top-right corner removes it.

// pictureRemoveSize is the ✕'s round button; it sits over the corner, half
// out of the picture, so even a tiny picture keeps it clear.
const pictureRemoveSize = 16

type pictureTile struct {
	widget.BaseWidget
	img     image.Image
	natural fyne.Size // its size when there is room
	onTap   func()
	remove  *tapRow // nil without one
}

func newPictureTile(img image.Image, natural fyne.Size, onTap, onRemove func()) *pictureTile {
	tile := &pictureTile{img: img, natural: natural, onTap: onTap}
	if onRemove != nil {
		tile.remove = newIconButton(iconClose, color.NRGBA{A: 200}, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, onRemove)
		tile.remove.iconPx, tile.remove.padPx = 10, 3
	}
	tile.ExtendBaseWidget(tile)
	return tile
}

// margin is the room the ✕ takes above and right of the picture.
func (tile *pictureTile) margin() float32 {
	if tile.remove == nil {
		return 0
	}
	return pictureRemoveSize / 2
}

func (tile *pictureTile) Tapped(*fyne.PointEvent) {
	if tile.onTap != nil {
		tile.onTap()
	}
}

func (tile *pictureTile) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (tile *pictureTile) MinSize() fyne.Size {
	m := tile.margin()
	return tile.natural.AddWidthHeight(m, m)
}

func (tile *pictureTile) CreateRenderer() fyne.WidgetRenderer {
	view := canvas.NewImageFromImage(tile.img)
	view.FillMode = canvas.ImageFillStretch // sized to its own shape by Layout
	view.ScaleMode = canvas.ImageScaleSmooth
	renderer := &pictureTileRenderer{tile: tile, view: view}
	return renderer
}

type pictureTileRenderer struct {
	tile *pictureTile
	view *canvas.Image
}

func (renderer *pictureTileRenderer) Layout(size fyne.Size) {
	tile := renderer.tile
	m := tile.margin()
	// A narrower row shrinks the picture, keeping its shape.
	room := size.SubtractWidthHeight(m, m)
	shown := tile.natural
	if room.Width < shown.Width || room.Height < shown.Height {
		scale := fyne.Max(0, fyne.Min(room.Width/shown.Width, room.Height/shown.Height))
		shown = fyne.NewSize(shown.Width*scale, shown.Height*scale)
	}
	renderer.view.Move(fyne.NewPos(0, m))
	renderer.view.Resize(shown)
	renderer.view.CornerRadius = fyne.Min(8, fyne.Min(shown.Width, shown.Height)/4)
	if tile.remove != nil {
		tile.remove.Resize(fyne.NewSquareSize(pictureRemoveSize))
		tile.remove.Move(fyne.NewPos(shown.Width+m-pictureRemoveSize, 0))
	}
}

func (renderer *pictureTileRenderer) MinSize() fyne.Size { return renderer.tile.MinSize() }
func (renderer *pictureTileRenderer) Refresh() {
	renderer.view.Image = renderer.tile.img
	renderer.Layout(renderer.tile.Size())
	renderer.view.Refresh()
}
func (renderer *pictureTileRenderer) Destroy() {}
func (renderer *pictureTileRenderer) Objects() []fyne.CanvasObject {
	if renderer.tile.remove != nil {
		return []fyne.CanvasObject{renderer.view, renderer.tile.remove}
	}
	return []fyne.CanvasObject{renderer.view}
}

// ---------------------------------------------------------------------------
// pictureStrip: a message's pictures, side by side, wrapping onto the next
// line when the message is too narrow; one wider than the line shrinks.

type pictureStrip struct {
	inset float32 // around the pictures
	gap   float32
	width float32 // the width of the last layout, to give the matching height
}

func newPictureStrip(inset float32) *fyne.Container {
	return container.New(&pictureStrip{inset: inset, gap: 6})
}

// natural is the strip's width with all its pictures on one line.
func (strip *pictureStrip) natural(objects []fyne.CanvasObject) float32 {
	width := 2 * strip.inset
	for i, object := range visibleObjects(objects) {
		if i > 0 {
			width += strip.gap
		}
		width += object.MinSize().Width
	}
	return width
}

// place gives each picture its rectangle within width, and the height used.
func (strip *pictureStrip) place(objects []fyne.CanvasObject, width float32, apply bool) float32 {
	x, y, line := float32(0), float32(0), float32(0)
	for _, object := range visibleObjects(objects) {
		size := object.MinSize()
		if width > 0 && size.Width > width { // alone wider than the line: smaller, same shape
			size = fyne.NewSize(width, size.Height*width/size.Width)
		}
		if x > 0 && width > 0 && x+size.Width > width {
			x, y, line = 0, y+line+strip.gap, 0
		}
		if apply {
			object.Move(fyne.NewPos(strip.inset+x, strip.inset+y))
			object.Resize(size)
		}
		x += size.Width + strip.gap
		line = fyne.Max(line, size.Height)
	}
	return y + line
}

func (strip *pictureStrip) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	strip.width = size.Width
	strip.place(objects, fyne.Max(1, size.Width-2*strip.inset), true)
}

func (strip *pictureStrip) MinSize(objects []fyne.CanvasObject) fyne.Size {
	width := float32(0) // not laid out yet: all on one line
	if strip.width > 0 {
		width = fyne.Max(1, strip.width-2*strip.inset)
	}
	return fyne.NewSize(2*strip.inset, strip.place(objects, width, false)+2*strip.inset)
}

func visibleObjects(objects []fyne.CanvasObject) []fyne.CanvasObject {
	out := objects[:0:0]
	for _, object := range objects {
		if object.Visible() {
			out = append(out, object)
		}
	}
	return out
}

// pictureStripWidth is the width a container of pictures asks for, on one
// line; 0 for anything else.
func pictureStripWidth(pictures *fyne.Container) float32 {
	if pictures == nil || !pictures.Visible() {
		return 0
	}
	if strip, ok := pictures.Layout.(*pictureStrip); ok {
		return strip.natural(pictures.Objects)
	}
	return 0
}

// ---------------------------------------------------------------------------
// pictureViewer: a picture over the whole window, as large as fits, with
// zoom (wheel, buttons, + − 0 keys), moving a zoomed picture by dragging
// it, copy (Ctrl+C) and save as. Escape or a click beside it closes it.

const (
	viewerMinZoom  = 0.05
	viewerMaxZoom  = 32
	viewerMargin   = 24
	viewerBarSpace = 64 // the toolbar's band at the top
)

type pictureViewer struct {
	widget.BaseWidget
	ui     *desktopUI
	canvas fyne.Canvas
	title  string
	img    image.Image
	fit    bool          // follows the window: as large as fits, never enlarged
	zoom   float32       // image pixels to device pixels, when not fit
	offset fyne.Position // the picture's centre from the view's centre

	dim     *canvas.Rectangle
	band    *canvas.Rectangle // behind the toolbar, when a zoomed picture reaches it
	view    *canvas.Image
	bar     *fyne.Container
	heading *fyne.Container
	percent *canvas.Text
	copy    *tapRow
	layer   *fyne.Container // the window's pictureLayer it is shown in; nil over the whole canvas
}

// openPicture shows part in the viewer; nothing when it cannot be decoded.
func (ui *desktopUI) openPicture(title string, part core.ImagePart) {
	img := decodePicture(part)
	if img == nil {
		ui.notice(title, "This picture can no longer be shown.")
		return
	}
	ui.openImage(title, img)
}

func (ui *desktopUI) openImage(title string, img image.Image) *pictureViewer {
	viewer := &pictureViewer{ui: ui, canvas: ui.window.Canvas(), title: title, img: img, fit: true, zoom: 1}
	viewer.ExtendBaseWidget(viewer)
	if layer := ui.windowOf().pictureLayer; layer != nil {
		// Under 1loop's own title bar: its buttons and its drag area stay
		// in reach, the window can still be moved, shrunk or closed.
		viewer.layer = layer
		layer.Add(viewer)
		layer.Show()
	} else {
		viewer.Resize(viewer.canvas.Size())
		viewer.canvas.Overlays().Add(viewer)
	}
	viewer.canvas.Focus(viewer)
	return viewer
}

func (viewer *pictureViewer) close() {
	if viewer.layer == nil {
		viewer.canvas.Overlays().Remove(viewer)
		return
	}
	viewer.layer.Remove(viewer)
	if len(viewer.layer.Objects) == 0 {
		viewer.layer.Hide()
	}
	viewer.canvas.Unfocus()
}

// underTitleBar lays the picture viewer out over the window, below the
// title bar drawn by 1loop (desktopUI.titleBar).
type underTitleBar struct{ ui *desktopUI }

func (under underTitleBar) top() float32 {
	if bar := under.ui.titleBar; bar != nil && bar.Visible() {
		return bar.Size().Height
	}
	return 0
}

func (under underTitleBar) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	top := under.top()
	for _, object := range objects {
		object.Move(fyne.NewPos(0, top))
		object.Resize(fyne.NewSize(size.Width, fyne.Max(0, size.Height-top)))
	}
}

func (under underTitleBar) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }

// deviceScale is how many device pixels a Fyne unit is.
func (viewer *pictureViewer) deviceScale() float32 {
	if scale := viewer.canvas.Scale(); scale > 0 {
		return scale
	}
	return 1
}

// area is the room the picture is fitted in, and its centre.
func (viewer *pictureViewer) area() (fyne.Size, fyne.Position) {
	size := viewer.Size()
	room := fyne.NewSize(fyne.Max(1, size.Width-2*viewerMargin), fyne.Max(1, size.Height-viewerBarSpace-viewerMargin))
	return room, fyne.NewPos(size.Width/2, viewerBarSpace+room.Height/2)
}

// fitZoom is the zoom at which the picture fits the window, at most 100 %.
func (viewer *pictureViewer) fitZoom() float32 {
	room, _ := viewer.area()
	b := viewer.img.Bounds()
	device := viewer.deviceScale()
	return fyne.Min(1, fyne.Min(room.Width*device/float32(b.Dx()), room.Height*device/float32(b.Dy())))
}

func (viewer *pictureViewer) currentZoom() float32 {
	if viewer.fit {
		return viewer.fitZoom()
	}
	return viewer.zoom
}

// shownSize is the picture's size on screen, in Fyne units.
func (viewer *pictureViewer) shownSize() fyne.Size {
	b := viewer.img.Bounds()
	k := viewer.currentZoom() / viewer.deviceScale()
	return fyne.NewSize(float32(b.Dx())*k, float32(b.Dy())*k)
}

// clampOffset keeps a zoomed picture over its area: it can be moved to see
// each of its edges, no further, and one smaller than the area stays centred.
func (viewer *pictureViewer) clampOffset() {
	room, _ := viewer.area()
	shown := viewer.shownSize()
	limit := func(value, picture, space float32) float32 {
		if picture <= space {
			return 0
		}
		edge := (picture - space) / 2
		return fyne.Max(-edge, fyne.Min(edge, value))
	}
	viewer.offset = fyne.NewPos(limit(viewer.offset.X, shown.Width, room.Width), limit(viewer.offset.Y, shown.Height, room.Height))
}

// setZoom zooms keeping the point at (relative to the area's centre) still.
func (viewer *pictureViewer) setZoom(zoom float32, at fyne.Position) {
	old := viewer.currentZoom()
	zoom = fyne.Max(viewerMinZoom, fyne.Min(viewerMaxZoom, zoom))
	ratio := zoom / old
	viewer.offset = fyne.NewPos(at.X-(at.X-viewer.offset.X)*ratio, at.Y-(at.Y-viewer.offset.Y)*ratio)
	viewer.fit, viewer.zoom = false, zoom
	viewer.clampOffset()
	viewer.Refresh()
}

func (viewer *pictureViewer) zoomBy(factor float32) {
	viewer.setZoom(viewer.currentZoom()*factor, fyne.Position{})
}

func (viewer *pictureViewer) fitWindow() {
	viewer.fit, viewer.offset = true, fyne.Position{}
	viewer.Refresh()
}

func (viewer *pictureViewer) actualSize() {
	viewer.setZoom(1, fyne.Position{})
}

// imageRect is where the picture is drawn.
func (viewer *pictureViewer) imageRect() rect {
	_, centre := viewer.area()
	shown := viewer.shownSize()
	at := fyne.NewPos(centre.X+viewer.offset.X-shown.Width/2, centre.Y+viewer.offset.Y-shown.Height/2)
	return rect{at, fyne.NewPos(shown.Width, shown.Height)}
}

func (viewer *pictureViewer) Scrolled(e *fyne.ScrollEvent) {
	if e.Scrolled.DY == 0 {
		return
	}
	// 25 per notch of a wheel (a touchpad sends smaller steps): ×1.18 a notch.
	factor := float32(math.Pow(1.0066, float64(e.Scrolled.DY)))
	_, centre := viewer.area()
	viewer.setZoom(viewer.currentZoom()*factor, e.Position.Subtract(centre))
}

func (viewer *pictureViewer) Dragged(e *fyne.DragEvent) {
	viewer.offset = viewer.offset.Add(e.Dragged)
	viewer.clampOffset()
	viewer.Refresh()
}

func (viewer *pictureViewer) DragEnd() {}

// Tapped closes the viewer on a click beside the picture and its toolbar.
func (viewer *pictureViewer) Tapped(e *fyne.PointEvent) {
	if viewer.imageRect().contains(e.Position) {
		return
	}
	viewer.close()
}

func (viewer *pictureViewer) TappedSecondary(*fyne.PointEvent) {}

func (viewer *pictureViewer) FocusGained() {}
func (viewer *pictureViewer) FocusLost()   {}
func (viewer *pictureViewer) TypedRune(r rune) {
	switch r {
	case '+', '=':
		viewer.zoomBy(1.25)
	case '-':
		viewer.zoomBy(1 / 1.25)
	case '0':
		viewer.fitWindow()
	case '1':
		viewer.actualSize()
	}
}

func (viewer *pictureViewer) TypedKey(e *fyne.KeyEvent) {
	if e.Name == fyne.KeyEscape {
		viewer.close()
	}
}

func (viewer *pictureViewer) TypedShortcut(shortcut fyne.Shortcut) {
	if _, ok := shortcut.(*fyne.ShortcutCopy); ok {
		viewer.copyPicture()
	}
}

// pictureBytes is the picture as PNG, the format copied and saved.
func (viewer *pictureViewer) pictureBytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, viewer.img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (viewer *pictureViewer) copyPicture() {
	go func() {
		data, err := viewer.pictureBytes()
		if err == nil {
			err = copyImageToClipboard(data)
		}
		fyne.Do(func() {
			if err != nil {
				viewer.ui.notice("Copy picture", err.Error())
				return
			}
			viewer.copy.icon = iconCheck
			viewer.copy.Refresh()
			time.AfterFunc(1400*time.Millisecond, func() {
				fyne.Do(func() { viewer.copy.icon = iconCopy; viewer.copy.Refresh() })
			})
		})
	}()
}

func (viewer *pictureViewer) savePicture() {
	name := "image.png"
	if title := strings.ToLower(strings.TrimSpace(viewer.title)); title != "" && title != "image" {
		name = strings.ReplaceAll(title, " ", "-") + ".png"
	}
	viewer.ui.pick(pickRequest{title: "Save picture", start: viewer.ui.pickStart(), save: true, name: name,
		filterName: "PNG images", extensions: []string{".png"}}, func(paths []string) {
		path := paths[0]
		if filepath.Ext(path) == "" {
			path += ".png"
		}
		go func() {
			data, err := viewer.pictureBytes()
			if err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
			if err != nil {
				fyne.Do(func() { viewer.ui.notice("Save picture", err.Error()) })
			}
		}()
	})
}

func (viewer *pictureViewer) CreateRenderer() fyne.WidgetRenderer {
	viewer.dim = canvas.NewRectangle(color.NRGBA{A: 220})
	viewer.band = canvas.NewRectangle(color.NRGBA{A: 200})
	viewer.view = canvas.NewImageFromImage(viewer.img)
	viewer.view.FillMode = canvas.ImageFillStretch
	button := func(icon fyne.Resource, onTap func()) *tapRow {
		row := newIconButton(icon, color.NRGBA{R: 255, G: 255, B: 255, A: 24}, color.NRGBA{R: 235, G: 235, B: 235, A: 255}, onTap)
		row.iconPx, row.padPx = 16, 8
		return row
	}
	viewer.percent = textLine("100 %", 13, color.NRGBA{R: 235, G: 235, B: 235, A: 255})
	viewer.percent.Alignment = fyne.TextAlignCenter
	percent := container.NewStack(newTapArea(viewer.actualSize), container.NewCenter(viewer.percent))
	viewer.copy = button(iconCopy, viewer.copyPicture)
	viewer.bar = container.New(layout.NewCustomPaddedHBoxLayout(6),
		button(iconZoomOut, func() { viewer.zoomBy(1 / 1.25) }),
		container.New(&fixedWidth{width: 52}, percent),
		button(iconZoomIn, func() { viewer.zoomBy(1.25) }),
		button(iconFit, viewer.fitWindow),
		viewer.copy,
		button(iconDownload, viewer.savePicture),
		button(iconClose, viewer.close))
	b := viewer.img.Bounds()
	title := textLine(viewer.title, 14, color.NRGBA{R: 235, G: 235, B: 235, A: 255})
	title.TextStyle = fyne.TextStyle{Bold: true}
	viewer.heading = container.NewHBox(title, textLine(fmt.Sprintf("%d × %d", b.Dx(), b.Dy()), 13, color.NRGBA{R: 170, G: 170, B: 170, A: 255}))
	return &pictureViewerRenderer{viewer}
}

func (viewer *pictureViewer) Cursor() desktop.Cursor { return desktop.DefaultCursor }

type pictureViewerRenderer struct{ viewer *pictureViewer }

func (renderer *pictureViewerRenderer) Layout(size fyne.Size) {
	viewer := renderer.viewer
	viewer.dim.Resize(size)
	viewer.clampOffset()
	box := viewer.imageRect()
	viewer.view.Move(box.pos)
	viewer.view.Resize(fyne.NewSize(box.size.X, box.size.Y))
	viewer.band.Resize(fyne.NewSize(size.Width, viewerBarSpace))
	viewer.band.Hidden = box.pos.Y >= viewerBarSpace
	// Enlarged pixels stay sharp: a small picture zoomed in shows them.
	if viewer.currentZoom() >= 2 {
		viewer.view.ScaleMode = canvas.ImageScalePixels
	} else {
		viewer.view.ScaleMode = canvas.ImageScaleSmooth
	}
	viewer.percent.Text = fmt.Sprintf("%d %%", int(math.Round(float64(viewer.currentZoom()*100))))
	viewer.percent.Refresh()
	bar := viewer.bar.MinSize()
	viewer.bar.Resize(bar)
	viewer.bar.Move(fyne.NewPos(size.Width-bar.Width-16, (viewerBarSpace-bar.Height)/2))
	heading := viewer.heading.MinSize()
	viewer.heading.Resize(fyne.NewSize(fyne.Max(0, fyne.Min(heading.Width, size.Width-bar.Width-56)), heading.Height))
	viewer.heading.Move(fyne.NewPos(20, (viewerBarSpace-heading.Height)/2))
}

func (renderer *pictureViewerRenderer) MinSize() fyne.Size { return fyne.Size{} }
func (renderer *pictureViewerRenderer) Refresh() {
	renderer.Layout(renderer.viewer.Size())
	canvas.Refresh(renderer.viewer.view)
}
func (renderer *pictureViewerRenderer) Destroy() {}
func (renderer *pictureViewerRenderer) Objects() []fyne.CanvasObject {
	viewer := renderer.viewer
	return []fyne.CanvasObject{viewer.dim, viewer.view, viewer.band, viewer.heading, viewer.bar}
}

// fixedWidth gives its one object a set width.
type fixedWidth struct{ width float32 }

func (fixed *fixedWidth) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Resize(size)
		object.Move(fyne.Position{})
	}
}

func (fixed *fixedWidth) MinSize(objects []fyne.CanvasObject) fyne.Size {
	height := float32(0)
	for _, object := range objects {
		height = fyne.Max(height, object.MinSize().Height)
	}
	return fyne.NewSize(fixed.width, height)
}
