package widget

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/scrollbar"
	"fyne.io/fyne/v2/theme"
)

// ScrollDirection represents the directions in which a Scroll can scroll its child content.
type ScrollDirection = fyne.ScrollDirection

// Constants for valid values of ScrollDirection.
const (
	// ScrollBoth supports horizontal and vertical scrolling.
	ScrollBoth ScrollDirection = iota
	// ScrollHorizontalOnly specifies the scrolling should only happen left to right.
	ScrollHorizontalOnly
	// ScrollVerticalOnly specifies the scrolling should only happen top to bottom.
	ScrollVerticalOnly
	// ScrollNone turns off scrolling for this container.
	//
	// Since: 2.0
	ScrollNone
)

type scrollBarOrientation int

// We default to vertical as 0 due to that being the original orientation offered
const (
	scrollBarOrientationVertical   scrollBarOrientation = 0
	scrollBarOrientationHorizontal scrollBarOrientation = 1
	scrollContainerMinSize                              = float32(32) // TODO consider the smallest useful scroll view?
)

// 1loop's bar (#633, package scrollbar): its area takes the pointer over
// scrollbar.Hit, wider than the thumb it draws; the thumb is never shorter
// than scrollbar.MinThumb and widens under the pointer over a faint track.
// Dragging the thumb or the track scrolls, a tap on the track pages, and
// the wheel over it goes on to the scroll.

var (
	_ desktop.Hoverable = (*scrollBarArea)(nil)
	_ fyne.Tappable     = (*scrollBarArea)(nil)
	_ fyne.Draggable    = (*scrollBarArea)(nil)
)

type scrollBarArea struct {
	Base

	isDragging  bool
	isMouseIn   bool
	scroll      *Scroll
	orientation scrollBarOrientation
	grab        float32 // where in the thumb a drag holds it

	// updated from renderer Layout
	// coordinates Y in vertical orientation, X in horizontal
	barLeadingEdge  float32
	barTrailingEdge float32
}

func (a *scrollBarArea) isLarge() bool {
	return a.isMouseIn || a.isDragging
}

// along is a point's coordinate along the bar.
func (a *scrollBarArea) along(p fyne.Position) float32 {
	if a.orientation == scrollBarOrientationHorizontal {
		return p.X
	}
	return p.Y
}

// metrics are the track's length and the scroll's view, content and offset
// along the bar.
func (a *scrollBarArea) metrics() (track, view, content, offset float32) {
	size, contentSize := a.scroll.Size(), a.scroll.Content.Size()
	if a.orientation == scrollBarOrientationHorizontal {
		return a.Size().Width - 2*scrollbar.Edge, size.Width, contentSize.Width, a.scroll.Offset.X
	}
	return a.Size().Height - 2*scrollbar.Edge, size.Height, contentSize.Height, a.scroll.Offset.Y
}

func (a *scrollBarArea) CreateRenderer() fyne.WidgetRenderer {
	track, thumb := canvas.NewRectangle(color.Transparent), canvas.NewRectangle(color.Transparent)
	r := &scrollBarAreaRenderer{BaseRenderer: NewBaseRenderer([]fyne.CanvasObject{track, thumb}), area: a, track: track, thumb: thumb}
	r.Refresh()
	return r
}

func (a *scrollBarArea) Cursor() desktop.Cursor {
	return desktop.DefaultCursor
}

// Tapped on the track pages towards the tap, keeping scrollbar.PageKeep of
// the view.
func (a *scrollBarArea) Tapped(e *fyne.PointEvent) {
	_, view, _, offset := a.metrics()
	switch at := a.along(e.Position); {
	case at < a.barLeadingEdge:
		a.scrollTo(offset - scrollbar.Page(view))
	case at > a.barTrailingEdge:
		a.scrollTo(offset + scrollbar.Page(view))
	}
}

// Dragged on the thumb moves it with the pointer; from the track, the
// thumb comes under the pointer by its middle.
func (a *scrollBarArea) Dragged(e *fyne.DragEvent) {
	track, view, content, offset := a.metrics()
	length, pos, ok := scrollbar.Thumb(track, view, content, offset)
	if !ok {
		return
	}
	at := a.along(e.Position) - scrollbar.Edge
	if !a.isDragging {
		a.isDragging = true
		press := at - a.along(fyne.NewPos(e.Dragged.DX, e.Dragged.DY))
		a.grab = scrollbar.Grab(press, length, pos)
	}
	a.scrollTo(scrollbar.OffsetFor(track, view, content, length, at-a.grab))
}

func (a *scrollBarArea) DragEnd() {
	a.isDragging = false
	if fyne.CurrentDevice().IsMobile() {
		a.MouseOut()
		return
	}
	a.scroll.refreshBars()
}

func (a *scrollBarArea) MouseIn(*desktop.MouseEvent) {
	a.isMouseIn = true
	a.scroll.refreshBars()
}

func (a *scrollBarArea) MouseMoved(*desktop.MouseEvent) {
}

func (a *scrollBarArea) MouseOut() {
	a.isMouseIn = false
	if a.isDragging {
		return
	}

	a.scroll.refreshBars()
}

// scrollTo moves the view to offset along the bar, within the content.
func (a *scrollBarArea) scrollTo(offset float32) {
	_, view, content, _ := a.metrics()
	offset = fyne.Min(fyne.Max(0, offset), fyne.Max(0, content-view))
	old := a.scroll.Offset
	if a.orientation == scrollBarOrientationHorizontal {
		a.scroll.Offset.X = offset
	} else {
		a.scroll.Offset.Y = offset
	}
	if f := a.scroll.OnScrolled; f != nil && a.scroll.Offset != old {
		f(a.scroll.Offset)
	}
	a.scroll.refreshWithoutOffsetUpdate()
}

// scrollBarHit is the area's width across the bar; 0 when the theme gives
// the bar no width: no bar then.
func scrollBarHit(th fyne.Theme) float32 {
	wide := th.Size(theme.SizeNameScrollBar)
	if wide <= 0 {
		return 0
	}
	return fyne.Max(scrollbar.Hit, wide+scrollbar.Edge+2)
}

type scrollBarAreaRenderer struct {
	BaseRenderer
	area         *scrollBarArea
	track, thumb *canvas.Rectangle
}

func (r *scrollBarAreaRenderer) theme() fyne.Theme {
	if custom := cache.WidgetTheme(r.area); custom != nil {
		return custom
	}
	if custom := cache.WidgetTheme(r.area.scroll.super()); custom != nil {
		return custom
	}
	return theme.Current()
}

func (r *scrollBarAreaRenderer) Layout(size fyne.Size) {
	r.layoutWithTheme(r.theme(), size)
}

func (r *scrollBarAreaRenderer) layoutWithTheme(th fyne.Theme, size fyne.Size) {
	width, _, _ := scrollbar.Look(th, r.area.isLarge())
	track, view, content, offset := r.area.metrics()
	length, pos, ok := scrollbar.Thumb(track, view, content, offset)
	r.thumb.Hidden = !ok
	r.area.barLeadingEdge = scrollbar.Edge + pos
	r.area.barTrailingEdge = scrollbar.Edge + pos + length
	switch r.area.orientation {
	case scrollBarOrientationHorizontal:
		y := size.Height - scrollbar.Edge - width
		r.track.Move(fyne.NewPos(scrollbar.Edge, y))
		r.track.Resize(fyne.NewSize(fyne.Max(0, track), width))
		r.thumb.Move(fyne.NewPos(r.area.barLeadingEdge, y))
		r.thumb.Resize(fyne.NewSize(length, width))
	default:
		x := size.Width - scrollbar.Edge - width
		r.track.Move(fyne.NewPos(x, scrollbar.Edge))
		r.track.Resize(fyne.NewSize(width, fyne.Max(0, track)))
		r.thumb.Move(fyne.NewPos(x, r.area.barLeadingEdge))
		r.thumb.Resize(fyne.NewSize(width, length))
	}
}

func (r *scrollBarAreaRenderer) MinSize() fyne.Size {
	hit := scrollBarHit(r.theme())
	return fyne.NewSize(hit, hit)
}

func (r *scrollBarAreaRenderer) Refresh() {
	th := r.theme()
	width, thumb, track := scrollbar.Look(th, r.area.isLarge())
	r.thumb.FillColor, r.track.FillColor = thumb, track
	r.thumb.CornerRadius, r.track.CornerRadius = width/2, width/2
	r.track.Hidden = !r.area.isLarge()
	r.layoutWithTheme(th, r.area.Size())
	canvas.Refresh(r.track)
	canvas.Refresh(r.thumb)
}

func newScrollBarArea(scroll *Scroll, orientation scrollBarOrientation) *scrollBarArea {
	a := &scrollBarArea{scroll: scroll, orientation: orientation}
	a.ExtendBaseWidget(a)
	return a
}

type scrollContainerRenderer struct {
	BaseRenderer
	scroll                  *Scroll
	vertArea                *scrollBarArea
	horizArea               *scrollBarArea
	leftShadow, rightShadow *Shadow
	topShadow, bottomShadow *Shadow
	oldMinSize              fyne.Size
}

func (r *scrollContainerRenderer) layoutBars(size fyne.Size) {
	scrollerSize := r.scroll.Size()
	// Where both bars show, each one's track ends before the other's area.
	vertCorner, horizCorner := float32(0), float32(0)
	if r.scroll.Direction == ScrollBoth && r.scroll.Content != nil {
		contentSize := r.scroll.Content.Size()
		if contentSize.Width > scrollerSize.Width {
			vertCorner = r.horizArea.MinSize().Height
		}
		if contentSize.Height > scrollerSize.Height {
			horizCorner = r.vertArea.MinSize().Width
		}
	}
	if r.scroll.Direction == ScrollVerticalOnly || r.scroll.Direction == ScrollBoth {
		r.vertArea.Resize(fyne.NewSize(r.vertArea.MinSize().Width, size.Height-vertCorner))
		r.vertArea.Move(fyne.NewPos(scrollerSize.Width-r.vertArea.Size().Width, 0))
		r.topShadow.Resize(fyne.NewSize(size.Width, 0))
		r.bottomShadow.Resize(fyne.NewSize(size.Width, 0))
		r.bottomShadow.Move(fyne.NewPos(0, scrollerSize.Height))
	}

	if r.scroll.Direction == ScrollHorizontalOnly || r.scroll.Direction == ScrollBoth {
		r.horizArea.Resize(fyne.NewSize(size.Width-horizCorner, r.horizArea.MinSize().Height))
		r.horizArea.Move(fyne.NewPos(0, scrollerSize.Height-r.horizArea.Size().Height))
		r.leftShadow.Resize(fyne.NewSize(0, size.Height))
		r.rightShadow.Resize(fyne.NewSize(0, size.Height))
		r.rightShadow.Move(fyne.NewPos(scrollerSize.Width, 0))
	}

	r.updatePosition()
}

func (r *scrollContainerRenderer) Layout(size fyne.Size) {
	c := r.scroll.Content
	c.Resize(c.MinSize().Max(size))

	r.layoutBars(size)
}

func (r *scrollContainerRenderer) MinSize() fyne.Size {
	return r.scroll.MinSize()
}

func (r *scrollContainerRenderer) Refresh() {
	r.horizArea.Refresh()
	r.vertArea.Refresh()
	r.leftShadow.Refresh()
	r.topShadow.Refresh()
	r.rightShadow.Refresh()
	r.bottomShadow.Refresh()

	if len(r.BaseRenderer.Objects()) == 0 || r.BaseRenderer.Objects()[0] != r.scroll.Content {
		// push updated content object to baseRenderer
		r.BaseRenderer.Objects()[0] = r.scroll.Content
	}
	size := r.scroll.Size()
	newMin := r.scroll.Content.MinSize()
	if r.oldMinSize == newMin && r.oldMinSize == r.scroll.Content.Size() &&
		(size.Width <= r.oldMinSize.Width && size.Height <= r.oldMinSize.Height) {
		r.layoutBars(size)
		return
	}

	r.oldMinSize = newMin
	r.Layout(size)
}

func (r *scrollContainerRenderer) handleAreaVisibility(contentSize, scrollSize float32, area *scrollBarArea) {
	if contentSize <= scrollSize || area.MinSize().Width <= 0 { // or no bar in this theme
		area.Hide()
	} else if r.scroll.Visible() {
		area.Show()
	}
}

func (r *scrollContainerRenderer) handleShadowVisibility(offset, contentSize, scrollSize float32, shadowStart fyne.CanvasObject, shadowEnd fyne.CanvasObject) {
	if !r.scroll.Visible() {
		return
	}
	if offset > 0 {
		shadowStart.Show()
	} else {
		shadowStart.Hide()
	}
	if offset < contentSize-scrollSize {
		shadowEnd.Show()
	} else {
		shadowEnd.Hide()
	}
}

func (r *scrollContainerRenderer) updatePosition() {
	if r.scroll.Content == nil {
		return
	}
	scrollSize := r.scroll.Size()
	contentSize := r.scroll.Content.Size()

	r.scroll.Content.Move(fyne.NewPos(-r.scroll.Offset.X, -r.scroll.Offset.Y))

	if r.scroll.Direction == ScrollVerticalOnly || r.scroll.Direction == ScrollBoth {
		r.handleAreaVisibility(contentSize.Height, scrollSize.Height, r.vertArea)
		r.handleShadowVisibility(r.scroll.Offset.Y, contentSize.Height, scrollSize.Height, r.topShadow, r.bottomShadow)
		cache.Renderer(r.vertArea).Layout(r.vertArea.Size())
	} else {
		r.vertArea.Hide()
		r.topShadow.Hide()
		r.bottomShadow.Hide()
	}
	if r.scroll.Direction == ScrollHorizontalOnly || r.scroll.Direction == ScrollBoth {
		r.handleAreaVisibility(contentSize.Width, scrollSize.Width, r.horizArea)
		r.handleShadowVisibility(r.scroll.Offset.X, contentSize.Width, scrollSize.Width, r.leftShadow, r.rightShadow)
		cache.Renderer(r.horizArea).Layout(r.horizArea.Size())
	} else {
		r.horizArea.Hide()
		r.leftShadow.Hide()
		r.rightShadow.Hide()
	}

	if r.scroll.Direction != ScrollHorizontalOnly {
		canvas.Refresh(r.vertArea) // this is required to force the canvas to update, we have no "Redraw()"
	} else {
		canvas.Refresh(r.horizArea) // this is required like above but if we are horizontal
	}
}

// Scroll defines a container that is smaller than the Content.
// The Offset is used to determine the position of the child widgets within the container.
type Scroll struct {
	Base
	minSize   fyne.Size
	Direction ScrollDirection
	Content   fyne.CanvasObject
	Offset    fyne.Position
	// OnScrolled can be set to be notified when the Scroll has changed position.
	// You should not update the Scroll.Offset from this method.
	//
	// Since: 2.0
	OnScrolled func(fyne.Position) `json:"-"`
}

// CreateRenderer is a private method to Fyne which links this widget to its renderer
func (s *Scroll) CreateRenderer() fyne.WidgetRenderer {
	scr := &scrollContainerRenderer{
		BaseRenderer: NewBaseRenderer([]fyne.CanvasObject{s.Content}),
		scroll:       s,
	}
	scr.vertArea = newScrollBarArea(s, scrollBarOrientationVertical)
	scr.topShadow = NewShadow(ShadowBottom, SubmergedContentLevel)
	scr.bottomShadow = NewShadow(ShadowTop, SubmergedContentLevel)
	scr.horizArea = newScrollBarArea(s, scrollBarOrientationHorizontal)
	scr.leftShadow = NewShadow(ShadowRight, SubmergedContentLevel)
	scr.rightShadow = NewShadow(ShadowLeft, SubmergedContentLevel)
	scr.SetObjects(append(scr.Objects(), scr.topShadow, scr.bottomShadow, scr.leftShadow, scr.rightShadow,
		scr.vertArea, scr.horizArea))
	for _, child := range []fyne.CanvasObject{scr.topShadow, scr.bottomShadow, scr.leftShadow, scr.rightShadow, scr.vertArea, scr.horizArea} {
		cache.OverrideThemeMatchingScope(child, s.super())
	}
	scr.updatePosition()

	return scr
}

// Bars are the scroll's bar areas that show, in the scroll's coordinates:
// for a layer drawn over the scroll that passes the pointer on to what is
// below it (1loop's text selection over a code block).
func (s *Scroll) Bars() []fyne.CanvasObject {
	r, ok := cache.Renderer(s.super()).(*scrollContainerRenderer)
	if !ok {
		return nil
	}
	var bars []fyne.CanvasObject
	for _, area := range []*scrollBarArea{r.vertArea, r.horizArea} {
		if area.Visible() {
			bars = append(bars, area)
		}
	}
	return bars
}

// ScrollToBottom will scroll content to container bottom - to show latest info which end user just added
func (s *Scroll) ScrollToBottom() {
	s.scrollBy(0, -1*(s.Content.MinSize().Height-s.Size().Height-s.Offset.Y))
	s.refreshBars()
}

// ScrollToTop will scroll content to container top
func (s *Scroll) ScrollToTop() {
	s.ScrollToOffset(fyne.Position{})
	s.refreshBars()
}

// MinSize returns the smallest size this widget can shrink to
func (s *Scroll) MinSize() fyne.Size {
	min := fyne.NewSize(scrollContainerMinSize, scrollContainerMinSize).Max(s.minSize)
	switch s.Direction {
	case ScrollHorizontalOnly:
		min.Height = fyne.Max(min.Height, s.Content.MinSize().Height)
	case ScrollVerticalOnly:
		min.Width = fyne.Max(min.Width, s.Content.MinSize().Width)
	case ScrollNone:
		return s.Content.MinSize()
	}
	return min
}

// SetMinSize specifies a minimum size for this scroll container.
// If the specified size is larger than the content size then scrolling will not be enabled
// This can be helpful to appear larger than default if the layout is collapsing this widget.
func (s *Scroll) SetMinSize(size fyne.Size) {
	s.minSize = size
}

// Refresh causes this widget to be redrawn in it's current state
func (s *Scroll) Refresh() {
	s.refreshBars()

	if s.Content != nil {
		s.Content.Refresh()
	}
}

// Resize is called when this scroller should change size. We refresh to ensure the scroll bars are updated.
func (s *Scroll) Resize(sz fyne.Size) {
	if sz == s.Size() {
		return
	}

	s.Base.Resize(sz)
	s.refreshBars()
}

// ScrollToOffset will update the location of the content of this scroll container.
//
// Since: 2.6
func (s *Scroll) ScrollToOffset(p fyne.Position) {
	if s.Offset == p {
		return
	}

	s.Offset = p
	s.refreshBars()
}

func (s *Scroll) refreshWithoutOffsetUpdate() {
	s.Base.Refresh()
}

// Scrolled is called when an input device triggers a scroll event
func (s *Scroll) Scrolled(ev *fyne.ScrollEvent) {
	if s.Direction != ScrollNone {
		s.scrollBy(ev.Scrolled.DX, ev.Scrolled.DY)
	}
}

func (s *Scroll) refreshBars() {
	s.updateOffset(0, 0)
	s.refreshWithoutOffsetUpdate()
}

func (s *Scroll) scrollBy(dx, dy float32) {
	min := s.Content.MinSize()
	size := s.Size()
	if size.Width < min.Width && size.Height >= min.Height && dx == 0 {
		dx, dy = dy, dx
	}
	if s.updateOffset(dx, dy) {
		s.refreshWithoutOffsetUpdate()
	}
}

func (s *Scroll) updateOffset(deltaX, deltaY float32) bool {
	size := s.Size()
	contentSize := s.Content.Size()
	if contentSize.Width <= size.Width && contentSize.Height <= size.Height {
		if s.Offset.X != 0 || s.Offset.Y != 0 {
			s.Offset.X = 0
			s.Offset.Y = 0
			return true
		}
		return false
	}
	oldX := s.Offset.X
	oldY := s.Offset.Y
	min := s.Content.MinSize()
	s.Offset.X = computeOffset(s.Offset.X, -deltaX, size.Width, min.Width)
	s.Offset.Y = computeOffset(s.Offset.Y, -deltaY, size.Height, min.Height)

	moved := s.Offset.X != oldX || s.Offset.Y != oldY
	if f := s.OnScrolled; f != nil && moved {
		f(s.Offset)
	}
	return moved
}

func computeOffset(start, delta, outerWidth, innerWidth float32) float32 {
	offset := start + delta
	if offset+outerWidth >= innerWidth {
		offset = innerWidth - outerWidth
	}

	return fyne.Max(offset, 0)
}

// NewScroll creates a scrollable parent wrapping the specified content.
// Note that this may cause the MinSize to be smaller than that of the passed object.
func NewScroll(content fyne.CanvasObject) *Scroll {
	s := newScrollContainerWithDirection(ScrollBoth, content)
	s.ExtendBaseWidget(s)
	return s
}

// NewHScroll create a scrollable parent wrapping the specified content.
// Note that this may cause the MinSize.Width to be smaller than that of the passed object.
func NewHScroll(content fyne.CanvasObject) *Scroll {
	s := newScrollContainerWithDirection(ScrollHorizontalOnly, content)
	s.ExtendBaseWidget(s)
	return s
}

// NewVScroll create a scrollable parent wrapping the specified content.
// Note that this may cause the MinSize.Height to be smaller than that of the passed object.
func NewVScroll(content fyne.CanvasObject) *Scroll {
	s := newScrollContainerWithDirection(ScrollVerticalOnly, content)
	s.ExtendBaseWidget(s)
	return s
}

func newScrollContainerWithDirection(direction ScrollDirection, content fyne.CanvasObject) *Scroll {
	s := &Scroll{
		Direction: direction,
		Content:   content,
	}
	s.ExtendBaseWidget(s)
	return s
}
