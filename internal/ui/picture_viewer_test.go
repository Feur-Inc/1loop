// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// Pictures keep their shape, fit their box and are never enlarged (#435).
func TestFitPictureBox(t *testing.T) {
	for _, c := range []struct {
		w, h       int
		maxW, maxH float32
		want       fyne.Size
	}{
		{300, 1200, messagePictureWidth, messagePictureHeight, fyne.NewSize(40, 160)},   // tall
		{1600, 200, messagePictureWidth, messagePictureHeight, fyne.NewSize(240, 30)},   // wide
		{12, 12, messagePictureWidth, messagePictureHeight, fyne.NewSize(12, 12)},       // tiny: as is
		{800, 800, composerPictureWidth, composerPictureHeight, fyne.NewSize(64, 64)},   // square
		{1600, 200, composerPictureWidth, composerPictureHeight, fyne.NewSize(192, 24)}, // wide, in the composer
		{0, 10, 100, 100, fyne.Size{}},
	} {
		got := fitPictureBox(c.w, c.h, c.maxW, c.maxH)
		if abs32(got.Width-c.want.Width) > 0.001 || abs32(got.Height-c.want.Height) > 0.001 {
			t.Errorf("%d × %d in %v × %v: %v, want %v", c.w, c.h, c.maxW, c.maxH, got, c.want)
		}
		if got.Width > c.maxW || got.Height > c.maxH {
			t.Errorf("%d × %d overflows its box: %v", c.w, c.h, got)
		}
	}
}

func testTile(w, h float32, remove bool) *pictureTile {
	var onRemove func()
	if remove {
		onRemove = func() {}
	}
	return newPictureTile(image.NewNRGBA(image.Rect(0, 0, int(w), int(h))), fyne.NewSize(w, h), nil, onRemove)
}

// A message's pictures stay inside it whatever its width: they wrap onto
// new lines, and one wider than the line shrinks with its shape kept.
func TestPictureStripNeverOverflows(t *testing.T) {
	test.NewTempApp(t)
	tiles := []*pictureTile{testTile(40, 160, false), testTile(240, 30, false), testTile(12, 12, false)}
	strip := newPictureStrip(4)
	for _, tile := range tiles {
		strip.Add(tile)
	}
	if got := pictureStripWidth(strip); got != 4+40+6+240+6+12+4 {
		t.Fatalf("natural width %v", got)
	}
	for _, width := range []float32{400, 200, 120, 30} {
		strip.Resize(fyne.NewSize(width, 1))
		height := strip.MinSize().Height
		for i, tile := range tiles {
			at, size := tile.Position(), tile.Size()
			if at.X < 0 || at.X+size.Width > width+0.01 || at.Y+size.Height > height+0.01 {
				t.Errorf("width %v: picture %d at %v size %v leaves the strip (height %v)", width, i, at, size, height)
			}
			ratio := tile.natural.Width / tile.natural.Height
			if got := size.Width / size.Height; got < ratio*0.99 || got > ratio*1.01 {
				t.Errorf("width %v: picture %d lost its shape: %v", width, i, size)
			}
		}
	}
}

// A message's bubble is as wide as its pictures, so they no longer run
// over its edge when its text is short.
func TestBubbleFitsItsPictures(t *testing.T) {
	test.NewTempApp(t)
	strip := newPictureStrip(4)
	strip.Add(testTile(240, 30, false))
	bubble := &bubbleLayout{pictures: strip, picChrom: 4}
	if got := bubble.width(1000); got != 4+240+4+4 {
		t.Errorf("bubble width %v", got)
	}
	if got := bubble.width(100); got != 78 {
		t.Errorf("narrow window: %v, want 78%% of it", got)
	}
}

// The composer's ✕ is a small button on the picture's top-right corner.
func TestRemoveButtonOnTheCorner(t *testing.T) {
	test.NewTempApp(t)
	for _, tile := range []*pictureTile{testTile(40, 64, true), testTile(12, 12, true)} {
		tile.Resize(tile.MinSize())
		test.WidgetRenderer(tile).Layout(tile.Size())
		button := tile.remove
		if button.Size() != fyne.NewSquareSize(pictureRemoveSize) {
			t.Errorf("✕ size %v", button.Size())
		}
		right, top := button.Position().X+button.Size().Width, button.Position().Y
		if right != tile.Size().Width || top != 0 {
			t.Errorf("✕ at %v in a %v tile: not on its top-right corner", button.Position(), tile.Size())
		}
		if !slices.Contains(test.WidgetRenderer(tile).Objects(), fyne.CanvasObject(button)) {
			t.Error("the ✕ is not drawn")
		}
	}
}

func testViewer(t *testing.T, w, h int) *pictureViewer {
	test.NewTempApp(t)
	window := test.NewWindow(nil)
	t.Cleanup(window.Close)
	viewer := &pictureViewer{canvas: window.Canvas(), img: image.NewNRGBA(image.Rect(0, 0, w, h)), fit: true, zoom: 1}
	viewer.ExtendBaseWidget(viewer)
	viewer.Resize(fyne.NewSize(1000, 700))
	test.WidgetRenderer(viewer)
	return viewer
}

func inside(inner, outer rect) bool {
	const e = 0.01
	return inner.pos.X >= outer.pos.X-e && inner.pos.Y >= outer.pos.Y-e &&
		inner.pos.X+inner.size.X <= outer.pos.X+outer.size.X+e && inner.pos.Y+inner.size.Y <= outer.pos.Y+outer.size.Y+e
}

// Opened, a picture fits the window and is never enlarged.
func TestViewerFits(t *testing.T) {
	for _, size := range [][2]int{{300, 1200}, {1600, 200}, {12, 12}, {4000, 3000}} {
		viewer := testViewer(t, size[0], size[1])
		room, centre := viewer.area()
		area := rect{fyne.NewPos(centre.X-room.Width/2, centre.Y-room.Height/2), fyne.NewPos(room.Width, room.Height)}
		if box := viewer.imageRect(); !inside(box, area) {
			t.Errorf("%v: %v leaves the area %v", size, box, area)
		}
		if viewer.currentZoom() > 1 {
			t.Errorf("%v: enlarged to %v", size, viewer.currentZoom())
		}
	}
	if viewer := testViewer(t, 12, 12); viewer.shownSize() != fyne.NewSize(12, 12) {
		t.Errorf("a tiny picture shows at %v", viewer.shownSize())
	}
}

// Zooming keeps the point under the pointer still, and a zoomed picture
// can be moved to its edges but not away from the window.
func TestViewerZoomAndPan(t *testing.T) {
	viewer := testViewer(t, 1600, 1200)
	_, centre := viewer.area()
	at := fyne.NewPos(100, -50)
	before := viewer.imageRect()
	pixel := func(box rect) fyne.Position { // the picture's point under at, as a fraction
		return fyne.NewPos((centre.X+at.X-box.pos.X)/box.size.X, (centre.Y+at.Y-box.pos.Y)/box.size.Y)
	}
	want := pixel(before)
	viewer.setZoom(viewer.currentZoom()*3, at)
	if got := pixel(viewer.imageRect()); abs32(got.X-want.X) > 0.001 || abs32(got.Y-want.Y) > 0.001 {
		t.Errorf("the point under the pointer moved: %v, then %v", want, got)
	}

	viewer.Dragged(&fyne.DragEvent{Dragged: fyne.NewDelta(-100000, 100000)})
	room, _ := viewer.area()
	box := viewer.imageRect()
	if box.pos.X+box.size.X < centre.X+room.Width/2-0.01 || box.pos.Y > centre.Y-room.Height/2+0.01 {
		t.Errorf("dragged off the window: %v", box)
	}

	viewer.fitWindow()
	if viewer.offset != (fyne.Position{}) || !viewer.fit {
		t.Errorf("fit kept offset %v", viewer.offset)
	}
	viewer.setZoom(1000, fyne.Position{})
	if viewer.zoom != viewerMaxZoom {
		t.Errorf("zoom %v past the limit", viewer.zoom)
	}
}

// Save as asks each desktop's own dialog where to write the picture.
func TestSaveChooserArguments(t *testing.T) {
	request := pickRequest{title: "Save picture", start: "/home/me", save: true, name: "image.png", filterName: "PNG images", extensions: []string{".png"}}
	zenity := strings.Join(zenityArgs(request), " ")
	for _, part := range []string{"--save", "--confirm-overwrite", "--filename=/home/me/image.png", "--file-filter=PNG images | *.png"} {
		if !strings.Contains(zenity, part) {
			t.Errorf("zenity %q lacks %q", zenity, part)
		}
	}
	if strings.Contains(zenity, "--multiple") {
		t.Errorf("zenity %q", zenity)
	}
	kdialog := kdialogArgs(request)
	if !slices.Contains(kdialog, "--getsavefilename") || !slices.Contains(kdialog, "/home/me/image.png") {
		t.Errorf("kdialog %q", kdialog)
	}
}

func TestFileURIPath(t *testing.T) {
	if got := fileURIPath("file:///home/me/My%20pictures/a.png"); got != "/home/me/My pictures/a.png" {
		t.Errorf("got %q", got)
	}
}
