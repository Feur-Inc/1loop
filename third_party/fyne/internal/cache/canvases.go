package cache

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/async"
)

var canvases async.Map[fyne.CanvasObject, *canvasInfo]

// GetCanvasForObject returns the canvas for the specified object.
func GetCanvasForObject(obj fyne.CanvasObject) fyne.Canvas {
	cinfo, ok := canvases.Load(obj)
	if cinfo == nil || !ok {
		return nil
	}
	cinfo.setAlive()
	return cinfo.canvas
}

// SetCanvasForObject sets the canvas for the specified object.
// The passed function will be called if the item was not previously attached to this canvas
//
// 1loop: an entry already there for this canvas is renewed in place. It
// was kept as it was (LoadOrStore), so during a long run of frames the
// entries of objects nobody refreshed expired after a minute, and each
// frame allocated an entry per object only to drop it; 1loop renewed them
// all with a walk of the window every few seconds.
func SetCanvasForObject(obj fyne.CanvasObject, c fyne.Canvas, setup func()) {
	if old, found := canvases.Load(obj); found && old != nil && old.canvas == c {
		old.setAlive()
		return
	}
	cinfo := &canvasInfo{canvas: c}
	cinfo.setAlive()

	old, found := canvases.LoadOrStore(obj, cinfo)
	if (!found || old.canvas != c) && setup != nil {
		setup()
	}
}

type canvasInfo struct {
	expiringCache
	canvas fyne.Canvas
}
