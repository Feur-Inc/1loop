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
	s := &stepSlider{steps: steps, index: index, pos: float32(index), onChanged: onChanged}
	s.ExtendBaseWidget(s)
	return s
}

const (
	sliderWidth = 38 // the vertical track, a pill
	sliderThumb = 30 // the thumb, inside the track
	sliderDot   = 6
	sliderInset = sliderWidth / 2 // the thumb's centre stays this far inside each end
)

func (s *stepSlider) span() float32 { return fyne.Max(1, s.Size().Height-2*sliderInset) }

func (s *stepSlider) stopY(i float32) float32 {
	if s.steps < 2 {
		return s.Size().Height - sliderInset
	}
	return s.Size().Height - sliderInset - s.span()*i/float32(s.steps-1)
}

func (s *stepSlider) stopAt(y float32) float32 {
	if s.steps < 2 {
		return 0
	}
	p := (s.Size().Height - sliderInset - y) / s.span() * float32(s.steps-1)
	return fyne.Min(float32(s.steps-1), fyne.Max(0, p))
}

// set moves to a stop with an eased glide and reports the change.
func (s *stepSlider) set(index int) {
	if s.disabled {
		return
	}
	index = int(math.Max(0, math.Min(float64(s.steps-1), float64(index))))
	s.glide(float32(index))
	if index != s.index {
		s.index = index
		if s.onChanged != nil {
			s.onChanged(index)
		}
	}
}

func (s *stepSlider) glide(to float32) {
	if s.anim != nil {
		s.anim.Stop()
	}
	from := s.pos
	s.anim = fyne.NewAnimation(240*time.Millisecond, func(p float32) {
		s.pos = from + (to-from)*p
		s.Refresh()
	})
	s.anim.Curve = easeOutQuint
	s.anim.Start()
}

func (s *stepSlider) Tapped(e *fyne.PointEvent) {
	s.set(int(math.Round(float64(s.stopAt(e.Position.Y)))))
}

func (s *stepSlider) Dragged(e *fyne.DragEvent) {
	if s.disabled {
		return
	}
	if s.anim != nil {
		s.anim.Stop()
	}
	s.dragging = true
	s.pos = s.stopAt(e.Position.Y)
	s.Refresh()
}

func (s *stepSlider) DragEnd() {
	s.dragging = false
	s.set(int(math.Round(float64(s.pos))))
}

func (s *stepSlider) Cursor() desktop.Cursor {
	if s.disabled {
		return desktop.DefaultCursor
	}
	return desktop.PointerCursor
}
func (s *stepSlider) MouseIn(*desktop.MouseEvent) {
	if !s.disabled {
		s.fadeHover(1)
	}
}
func (s *stepSlider) MouseMoved(*desktop.MouseEvent) {}
func (s *stepSlider) MouseOut()                      { s.fadeHover(0) }

func (s *stepSlider) fadeHover(to float32) {
	if s.hoverAnim != nil {
		s.hoverAnim.Stop()
	}
	from := s.hover
	s.hoverAnim = fyne.NewAnimation(140*time.Millisecond, func(p float32) {
		s.hover = from + (to-from)*p
		s.Refresh()
	})
	s.hoverAnim.Start()
}

// key handles the arrow keys of the popover that holds the slider.
func (s *stepSlider) key(e *fyne.KeyEvent) bool {
	switch e.Name {
	case fyne.KeyDown:
		s.set(s.index - 1)
	case fyne.KeyUp:
		s.set(s.index + 1)
	case fyne.KeyHome:
		s.set(0)
	case fyne.KeyEnd:
		s.set(s.steps - 1)
	default:
		return false
	}
	return true
}

func (s *stepSlider) CreateRenderer() fyne.WidgetRenderer {
	r := &stepSliderRenderer{s: s, track: canvas.NewRectangle(color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff}),
		fill: canvas.NewRectangle(colAmber), thumb: canvas.NewCircle(colText), shadow: canvas.NewCircle(color.NRGBA{A: 0x33})}
	r.track.CornerRadius = sliderWidth / 2
	r.fill.CornerRadius = sliderWidth / 2
	for i := 0; i < s.steps; i++ {
		r.dots = append(r.dots, canvas.NewCircle(color.Transparent))
	}
	r.Refresh() // colours the dots now, not at the first hover
	return r
}

type stepSliderRenderer struct {
	s           *stepSlider
	track, fill *canvas.Rectangle
	thumb       *canvas.Circle
	shadow      *canvas.Circle
	dots        []*canvas.Circle
}

func (r *stepSliderRenderer) MinSize() fyne.Size { return fyne.NewSize(sliderWidth, 202) }

func (r *stepSliderRenderer) Layout(size fyne.Size) {
	s := r.s
	r.track.Move(fyne.NewPos((size.Width-sliderWidth)/2, 0))
	r.track.Resize(fyne.NewSize(sliderWidth, size.Height))
	y := s.stopY(s.pos)
	r.fill.Move(fyne.NewPos(r.track.Position().X, y-sliderInset))
	r.fill.Resize(fyne.NewSize(sliderWidth, size.Height-y+sliderInset))
	for i, dot := range r.dots {
		dot.Move(fyne.NewPos(size.Width/2-sliderDot/2, s.stopY(float32(i))-sliderDot/2))
		dot.Resize(fyne.NewSquareSize(sliderDot))
	}
	thumb := sliderThumb + s.hover
	if s.dragging {
		thumb = sliderThumb + 1.5
	}
	at := fyne.NewPos(size.Width/2-thumb/2, y-thumb/2)
	r.thumb.Move(at)
	r.thumb.Resize(fyne.NewSquareSize(thumb))
	r.shadow.Move(at.AddXY(0, 2))
	r.shadow.Resize(fyne.NewSquareSize(thumb))
}

func (r *stepSliderRenderer) Refresh() {
	s := r.s
	r.Layout(s.Size())
	r.fill.Hidden = s.disabled
	r.thumb.FillColor = colText
	r.shadow.Hidden = s.disabled
	if s.disabled {
		r.thumb.FillColor = color.NRGBA{R: 0x77, G: 0x77, B: 0x77, A: 0xff}
	}
	for i, dot := range r.dots {
		// Dots under the amber part read as darker amber, the others as
		// faint white, like the stops of a web range input.
		if !s.disabled && float32(i) <= s.pos+0.01 {
			dot.FillColor = withAlpha(colInk, 0.35)
		} else {
			dot.FillColor = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x55}
		}
		dot.Refresh()
	}
	r.track.Refresh()
	r.fill.Refresh()
	r.shadow.Refresh()
	r.thumb.Refresh()
}

func (r *stepSliderRenderer) Objects() []fyne.CanvasObject {
	objects := []fyne.CanvasObject{r.track, r.fill}
	for _, dot := range r.dots {
		objects = append(objects, dot)
	}
	return append(objects, r.shadow, r.thumb)
}

func (r *stepSliderRenderer) Destroy() {}
