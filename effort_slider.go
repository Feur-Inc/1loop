package main

import (
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// stepSlider picks one of a few ordered levels on a pill-shaped track, like
// Codex: the amber part grows up to a white thumb that sits inside the
// track, and small dots mark the stops. The thumb glides to the chosen stop,
// follows the pointer while dragged and settles on the nearest stop.
type stepSlider struct {
	widget.BaseWidget
	steps     int
	index     int
	pos       float32 // thumb position, in stops (animated)
	hover     float32
	dragging  bool
	disabled  bool
	anim      *fyne.Animation
	hoverAnim *fyne.Animation
	onChanged func(int)
}

func newStepSlider(steps, index int, onChanged func(int)) *stepSlider {
	slider := &stepSlider{steps: steps, index: index, pos: float32(index), onChanged: onChanged}
	slider.ExtendBaseWidget(slider)
	return slider
}

const (
	sliderWidth = 38 // the vertical track, a pill
	sliderThumb = 30 // the thumb, inside the track
	sliderDot   = 6
	sliderInset = sliderWidth / 2 // the thumb's centre stays this far inside each end
)

func (slider *stepSlider) span() float32 { return fyne.Max(1, slider.Size().Height-2*sliderInset) }

func (slider *stepSlider) stopY(i float32) float32 {
	if slider.steps < 2 {
		return slider.Size().Height - sliderInset
	}
	return slider.Size().Height - sliderInset - slider.span()*i/float32(slider.steps-1)
}

func (slider *stepSlider) stopAt(y float32) float32 {
	if slider.steps < 2 {
		return 0
	}
	p := (slider.Size().Height - sliderInset - y) / slider.span() * float32(slider.steps-1)
	return fyne.Min(float32(slider.steps-1), fyne.Max(0, p))
}

// set moves to a stop with an eased glide and reports the change.
func (slider *stepSlider) set(index int) {
	if slider.disabled {
		return
	}
	index = int(math.Max(0, math.Min(float64(slider.steps-1), float64(index))))
	slider.glide(float32(index))
	if index != slider.index {
		slider.index = index
		if slider.onChanged != nil {
			slider.onChanged(index)
		}
	}
}

func (slider *stepSlider) glide(to float32) {
	if slider.anim != nil {
		slider.anim.Stop()
	}
	from := slider.pos
	slider.anim = fyne.NewAnimation(240*time.Millisecond, func(p float32) {
		slider.pos = from + (to-from)*p
		slider.Refresh()
	})
	slider.anim.Curve = easeOutQuint
	slider.anim.Start()
}

func (slider *stepSlider) Tapped(e *fyne.PointEvent) {
	slider.set(int(math.Round(float64(slider.stopAt(e.Position.Y)))))
}

func (slider *stepSlider) Dragged(e *fyne.DragEvent) {
	if slider.disabled {
		return
	}
	if slider.anim != nil {
		slider.anim.Stop()
	}
	slider.dragging = true
	slider.pos = slider.stopAt(e.Position.Y)
	slider.Refresh()
}

func (slider *stepSlider) DragEnd() {
	slider.dragging = false
	slider.set(int(math.Round(float64(slider.pos))))
}

func (slider *stepSlider) Cursor() desktop.Cursor {
	if slider.disabled {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}
func (slider *stepSlider) MouseIn(*desktop.MouseEvent) {
	if !slider.disabled {
		slider.fadeHover(1)
	}
}
func (slider *stepSlider) MouseMoved(*desktop.MouseEvent) {}
func (slider *stepSlider) MouseOut()                      { slider.fadeHover(0) }

func (slider *stepSlider) fadeHover(to float32) {
	if slider.hoverAnim != nil {
		slider.hoverAnim.Stop()
	}
	from := slider.hover
	slider.hoverAnim = fyne.NewAnimation(140*time.Millisecond, func(p float32) {
		slider.hover = from + (to-from)*p
		slider.Refresh()
	})
	slider.hoverAnim.Start()
}

// key handles the arrow keys of the popover that holds the slider.
func (slider *stepSlider) key(e *fyne.KeyEvent) bool {
	switch e.Name {
	case fyne.KeyDown:
		slider.set(slider.index - 1)
	case fyne.KeyUp:
		slider.set(slider.index + 1)
	case fyne.KeyHome:
		slider.set(0)
	case fyne.KeyEnd:
		slider.set(slider.steps - 1)
	default:
		return false
	}
	return true
}

func (slider *stepSlider) CreateRenderer() fyne.WidgetRenderer {
	renderer := &stepSliderRenderer{slider: slider, track: canvas.NewRectangle(color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff}),
		fill: canvas.NewRectangle(colAmber), thumb: canvas.NewCircle(colText), shadow: canvas.NewCircle(color.NRGBA{A: 0x33})}
	renderer.track.CornerRadius = sliderWidth / 2
	renderer.fill.CornerRadius = sliderWidth / 2
	for i := 0; i < slider.steps; i++ {
		renderer.dots = append(renderer.dots, canvas.NewCircle(color.Transparent))
	}
	renderer.Refresh() // colours the dots now, not at the first hover
	return renderer
}

type stepSliderRenderer struct {
	slider      *stepSlider
	track, fill *canvas.Rectangle
	thumb       *canvas.Circle
	shadow      *canvas.Circle
	dots        []*canvas.Circle
}

func (renderer *stepSliderRenderer) MinSize() fyne.Size { return fyne.NewSize(sliderWidth, 202) }

func (renderer *stepSliderRenderer) Layout(size fyne.Size) {
	slider := renderer.slider
	renderer.track.Move(fyne.NewPos((size.Width-sliderWidth)/2, 0))
	renderer.track.Resize(fyne.NewSize(sliderWidth, size.Height))
	y := slider.stopY(slider.pos)
	renderer.fill.Move(fyne.NewPos(renderer.track.Position().X, y-sliderInset))
	renderer.fill.Resize(fyne.NewSize(sliderWidth, size.Height-y+sliderInset))
	for i, dot := range renderer.dots {
		dot.Move(fyne.NewPos(size.Width/2-sliderDot/2, slider.stopY(float32(i))-sliderDot/2))
		dot.Resize(fyne.NewSquareSize(sliderDot))
	}
	thumb := sliderThumb + slider.hover
	if slider.dragging {
		thumb = sliderThumb + 1.5
	}
	at := fyne.NewPos(size.Width/2-thumb/2, y-thumb/2)
	renderer.thumb.Move(at)
	renderer.thumb.Resize(fyne.NewSquareSize(thumb))
	renderer.shadow.Move(at.AddXY(0, 2))
	renderer.shadow.Resize(fyne.NewSquareSize(thumb))
}

func (renderer *stepSliderRenderer) Refresh() {
	slider := renderer.slider
	renderer.Layout(slider.Size())
	renderer.fill.Hidden = slider.disabled
	renderer.thumb.FillColor = colText
	renderer.shadow.Hidden = slider.disabled
	if slider.disabled {
		renderer.thumb.FillColor = color.NRGBA{R: 0x77, G: 0x77, B: 0x77, A: 0xff}
	}
	for i, dot := range renderer.dots {
		// Dots under the amber part read as darker amber, the others as
		// faint white, like the stops of a web range input.
		if !slider.disabled && float32(i) <= slider.pos+0.01 {
			dot.FillColor = withAlpha(colInk, 0.35)
		} else {
			dot.FillColor = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x55}
		}
		dot.Refresh()
	}
	renderer.track.Refresh()
	renderer.fill.Refresh()
	renderer.shadow.Refresh()
	renderer.thumb.Refresh()
}

func (renderer *stepSliderRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{renderer.track, renderer.fill}
	for _, dot := range renderer.dots {
		objects = append(objects, dot)
	}
	return append(objects, renderer.shadow, renderer.thumb)
}

func (renderer *stepSliderRenderer) Destroy() {}
