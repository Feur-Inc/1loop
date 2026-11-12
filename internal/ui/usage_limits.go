// SPDX-License-Identifier: MPL-2.0

package ui

// The Usage page's subscription limits: each window a subscription reports
// (a week, 5 hours…), how much is left, the even pace that would last
// exactly until the reset, and where the current pace leads, drawn as the
// window went (internal/usage/limits.go). The phone shows the same
// (android Usage.kt).

import (
	"image/color"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/usage"
)

// limitsSection is every subscription's windows, a card per connection.
func limitsSection(limits []usage.Limit) fyne.CanvasObject {
	if len(limits) == 0 {
		return nil
	}
	section := container.NewVBox(sectionTitle("Subscription limits"),
		padded(0, 8, 2, 2, plainFlow("How much of each subscription window is left, and the pace that makes it last until it resets.", 12.5, colMuted)))
	var order []string
	byConnection := map[string][]usage.Limit{}
	for _, limit := range limits {
		if byConnection[limit.Connection] == nil {
			order = append(order, limit.Connection)
		}
		byConnection[limit.Connection] = append(byConnection[limit.Connection], limit)
	}
	for _, connection := range order {
		windows := byConnection[connection]
		name := windows[0].Name
		if name == "" {
			name = "Subscription"
		}
		title := textLine(name, 15, colText)
		title.TextStyle = fyne.TextStyle{Bold: true}
		rows := []fyne.CanvasObject{padded(14, 0, 16, 16, title)}
		for _, limit := range windows {
			if limit.Hours >= 24 {
				rows = append(rows, limitPace(limit))
			} else {
				rows = append(rows, limitLine(limit))
			}
		}
		section.Add(padded(0, 12, 0, 0, card(rows...)))
	}
	return section
}

// statusInk is the colour a window's status is said in.
func statusInk(status string) color.Color {
	switch status {
	case "out":
		return colDanger
	case "fast":
		return colAmber
	case "spare":
		return colAdded
	}
	return colTextSoft
}

// statusPill is a window's status, in its colour.
func statusPill(limit usage.Limit) fyne.CanvasObject {
	ink := statusInk(limit.Status)
	text := textLine(limit.StatusText, 12, ink)
	text.TextStyle = fyne.TextStyle{Bold: true}
	r, g, b, _ := ink.RGBA()
	fill := canvas.NewRectangle(color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 38})
	fill.CornerRadius = 11
	return container.NewStack(fill, padded(3, 3, 10, 10, text))
}

// statTile is one figure with what it is above and a word under it.
func statTile(label, value, under string, ink color.Color) fyne.CanvasObject {
	top := textLine(label, 12, colMuted)
	big := textLine(value, 22, ink)
	big.TextStyle = fyne.TextStyle{Bold: true}
	column := container.New(layout.NewCustomPaddedVBoxLayout(3), top, big)
	if under != "" {
		column.Add(plainFlow(under, 12, colMuted))
	}
	return column
}

func percentText(value float64) string { return usage.Percent(value) }

// limitPace is a long window (a day or more): its figures, its chart and
// what its pace means.
func limitPace(limit usage.Limit) fyne.CanvasObject {
	name := textLine(limit.Title, 14, colText)
	head := container.NewBorder(nil, nil, container.NewCenter(name), container.NewCenter(statusPill(limit)))
	leftInk := colText
	if limit.Status == "out" {
		leftInk = colDanger
	}
	gap := limit.Left - limit.Even
	evenUnder := "where an even pace would be"
	switch {
	case gap <= -1:
		evenUnder = usage.Points(-gap) + " more used than even"
	case gap >= 1:
		evenUnder = usage.Points(gap) + " less used than even"
	}
	tiles := container.NewGridWithColumns(4,
		statTile("Left", percentText(limit.Left), "of the window", leftInk),
		statTile("Even pace", percentText(limit.Even), evenUnder, colText),
		statTile("To last until reset", limit.Budget, "at most", colText),
		statTile("Resets", shortReset(limit), limit.ResetText, colText),
	)
	chart := newPaceChart(limit)
	return padded(12, 16, 16, 16, container.New(layout.NewCustomPaddedVBoxLayout(12),
		head, tiles, chart, plainFlow(limit.Advice, 13, colTextSoft)))
}

// shortReset is the reset's first words: "Sun 14:00" of "Sun 14:00 · in 2 d".
func shortReset(limit usage.Limit) string {
	when, _, _ := strings.Cut(limit.ResetText, " · ")
	return when
}

// limitLine is a short window (5 hours): a bar of what is left with the
// even pace marked on it, and its words.
func limitLine(limit usage.Limit) fyne.CanvasObject {
	name := textLine(limit.Title, 14, colText)
	when, in, _ := strings.Cut(limit.ResetText, " · ")
	figures := textLine(percentText(limit.Left)+" left · even pace "+percentText(limit.Even)+" · up to "+limit.Budget+" · resets "+when+" ("+in+")", 12.5, colMuted)
	head := container.NewBorder(nil, nil, container.NewCenter(name), container.NewCenter(statusPill(limit)))
	bar := newLeftBar(limit.Left, limit.Even, statusInk(limit.Status))
	return padded(12, 14, 16, 16, container.New(layout.NewCustomPaddedVBoxLayout(8), head, bar, figures))
}

// leftBar is a track filled to what is left, with a tick where an even
// pace would be.
type leftBar struct {
	widget.BaseWidget
	left, even float64
	ink        color.Color
}

func newLeftBar(left, even float64, ink color.Color) *leftBar {
	bar := &leftBar{left: left, even: even, ink: ink}
	bar.ExtendBaseWidget(bar)
	return bar
}

func (bar *leftBar) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(colTrack)
	track.CornerRadius = 3
	fill := canvas.NewRectangle(bar.ink)
	fill.CornerRadius = 3
	tick := canvas.NewRectangle(colText)
	return &leftBarRenderer{bar: bar, track: track, fill: fill, tick: tick}
}

type leftBarRenderer struct {
	bar               *leftBar
	track, fill, tick *canvas.Rectangle
}

func (r *leftBarRenderer) Layout(size fyne.Size) {
	r.track.Move(fyne.NewPos(0, 2))
	r.track.Resize(fyne.NewSize(size.Width, 6))
	r.fill.Move(fyne.NewPos(0, 2))
	r.fill.Resize(fyne.NewSize(size.Width*float32(r.bar.left/100), 6))
	r.tick.Move(fyne.NewPos(size.Width*float32(r.bar.even/100)-1, 0))
	r.tick.Resize(fyne.NewSize(2, 10))
	if r.bar.even < 0 { // a share, with no pace to mark
		r.tick.Hide()
	}
}
func (r *leftBarRenderer) MinSize() fyne.Size { return fyne.NewSize(120, 10) }
func (r *leftBarRenderer) Refresh()           {}
func (r *leftBarRenderer) Destroy()           {}
func (r *leftBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.fill, r.tick}
}

// paceChart draws a window from its start to its reset: what was left at
// each reading (solid), where the pace leads (dashed, in the same colour),
// and the even pace (dashed, faint), on 0–100% with the days marked.
type paceChart struct {
	widget.BaseWidget
	limit usage.Limit
	now   time.Time
}

func newPaceChart(limit usage.Limit) *paceChart {
	chart := &paceChart{limit: limit, now: time.Now()}
	chart.ExtendBaseWidget(chart)
	return chart
}

const (
	paceAxis   = 40 // the percents' column
	paceBottom = 22 // the days' row
	paceTop    = 18 // room for "Now"
	paceDashes = 48 // dashes in a dashed line, whatever its length
)

type paceRenderer struct {
	chart    *paceChart
	grid     []*canvas.Line
	percents []*canvas.Text
	days     []*canvas.Text
	dayAt    []time.Time
	even     []*canvas.Line
	recorded []*canvas.Line
	forecast []*canvas.Line
	nowLine  *canvas.Line
	nowText  *canvas.Text
	dot      *canvas.Circle
	legend   *fyne.Container
	objects  []fyne.CanvasObject
}

func dashes(ink color.Color, width float32) []*canvas.Line {
	lines := make([]*canvas.Line, paceDashes)
	for i := range lines {
		lines[i] = canvas.NewLine(ink)
		lines[i].StrokeWidth = width
	}
	return lines
}

func (chart *paceChart) CreateRenderer() fyne.WidgetRenderer {
	limit := chart.limit
	r := &paceRenderer{chart: chart}
	for _, value := range []int{100, 75, 50, 25, 0} {
		line := canvas.NewLine(colBorderSoft)
		line.StrokeWidth = 1
		r.grid = append(r.grid, line)
		text := textLine(percentText(float64(value)), 11, colFaint)
		text.Alignment = fyne.TextAlignTrailing
		r.percents = append(r.percents, text)
	}
	// A label at each midnight inside the window, at most eight.
	local := limit.Start.In(chart.now.Location())
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).AddDate(0, 0, 1)
	step := max(1, int(math.Ceil(limit.Hours/24/8)))
	for ; day.Before(limit.Reset); day = day.AddDate(0, 0, step) {
		text := textLine(day.Format("Mon"), 11, colFaint)
		text.Alignment = fyne.TextAlignCenter
		r.days, r.dayAt = append(r.days, text), append(r.dayAt, day)
	}
	r.even = dashes(colFaint, 1.5)
	for range max(0, len(limit.Points)-1) {
		line := canvas.NewLine(statusInk(limit.Status))
		line.StrokeWidth = 2.5
		r.recorded = append(r.recorded, line)
	}
	r.forecast = dashes(statusInk(limit.Status), 2)
	r.nowLine = canvas.NewLine(colBorder)
	r.nowLine.StrokeWidth = 1
	r.nowText = textLine("Now", 11, colMuted)
	r.nowText.TextStyle = fyne.TextStyle{Bold: true}
	r.dot = canvas.NewCircle(statusInk(limit.Status))
	r.legend = container.NewHBox(legendKey("Recorded", statusInk(limit.Status), false), spacer(10, 0),
		legendKey("Forecast", statusInk(limit.Status), true), spacer(10, 0), legendKey("Even pace", colFaint, true))
	for _, line := range r.grid {
		r.objects = append(r.objects, line)
	}
	for _, text := range r.percents {
		r.objects = append(r.objects, text)
	}
	for _, text := range r.days {
		r.objects = append(r.objects, text)
	}
	r.objects = append(r.objects, r.nowLine, r.nowText)
	for _, group := range [][]*canvas.Line{r.even, r.forecast, r.recorded} {
		for _, line := range group {
			r.objects = append(r.objects, line)
		}
	}
	r.objects = append(r.objects, r.dot, r.legend)
	return r
}

// legendKey is a short line in a colour, solid or dashed, and its name.
func legendKey(name string, ink color.Color, dashed bool) fyne.CanvasObject {
	mark := container.NewWithoutLayout()
	for i := 0; i < 3; i++ {
		if dashed && i == 1 {
			continue
		}
		segment := canvas.NewLine(ink)
		segment.StrokeWidth = 2
		segment.Position1 = fyne.NewPos(float32(i)*6, 8)
		segment.Position2 = fyne.NewPos(float32(i)*6+6, 8)
		if dashed {
			segment.Position2 = fyne.NewPos(float32(i)*6+4, 8)
		}
		mark.Add(segment)
	}
	return container.NewHBox(container.NewGridWrap(fyne.NewSize(18, 16), mark), container.NewCenter(textLine(name, 12, colMuted)))
}

func (r *paceRenderer) MinSize() fyne.Size {
	return fyne.NewSize(320, 210)
}

func (r *paceRenderer) Layout(size fyne.Size) {
	limit := r.chart.limit
	legendHeight := r.legend.MinSize().Height
	r.legend.Move(fyne.NewPos(paceAxis, 0))
	r.legend.Resize(r.legend.MinSize())
	top := legendHeight + paceTop
	plot := fyne.NewSize(size.Width-paceAxis-8, size.Height-top-paceBottom)
	start, total := limit.Start, limit.Reset.Sub(limit.Start)
	x := func(at time.Time) float32 {
		share := float64(at.Sub(start)) / float64(total)
		return paceAxis + plot.Width*float32(math.Max(0, math.Min(1, share)))
	}
	y := func(left float64) float32 {
		return top + plot.Height*float32(1-math.Max(0, math.Min(100, left))/100)
	}
	for i, line := range r.grid {
		value := 100 - float64(i)*25
		line.Position1, line.Position2 = fyne.NewPos(paceAxis, y(value)), fyne.NewPos(paceAxis+plot.Width, y(value))
		text := r.percents[i]
		text.Resize(fyne.NewSize(paceAxis-8, 14))
		text.Move(fyne.NewPos(0, y(value)-7))
	}
	for i, text := range r.days {
		text.Resize(fyne.NewSize(40, 14))
		text.Move(fyne.NewPos(x(r.dayAt[i])-20, top+plot.Height+6))
	}
	dashed := func(lines []*canvas.Line, from, to fyne.Position) {
		for i, line := range lines {
			a, b := float32(i)/paceDashes, (float32(i)+0.55)/paceDashes
			line.Position1 = fyne.NewPos(from.X+(to.X-from.X)*a, from.Y+(to.Y-from.Y)*a)
			line.Position2 = fyne.NewPos(from.X+(to.X-from.X)*b, from.Y+(to.Y-from.Y)*b)
		}
	}
	dashed(r.even, fyne.NewPos(x(start), y(100)), fyne.NewPos(x(limit.Reset), y(0)))
	for i, line := range r.recorded {
		a, b := limit.Points[i], limit.Points[i+1]
		line.Position1, line.Position2 = fyne.NewPos(x(a.Time), y(a.Left)), fyne.NewPos(x(b.Time), y(b.Left))
	}
	from := fyne.NewPos(x(limit.Checked), y(limit.Left))
	end := fyne.NewPos(x(limit.Reset), y(limit.LeftAtReset))
	if !limit.EmptyAt.IsZero() {
		end = fyne.NewPos(x(limit.EmptyAt), y(0))
	}
	dashed(r.forecast, from, end)
	now := x(r.chart.now)
	r.nowLine.Position1, r.nowLine.Position2 = fyne.NewPos(now, top-4), fyne.NewPos(now, top+plot.Height)
	r.nowText.Move(fyne.NewPos(now-r.nowText.MinSize().Width/2, top-paceTop))
	r.dot.Move(from.SubtractXY(4.5, 4.5))
	r.dot.Resize(fyne.NewSquareSize(9))
}

func (r *paceRenderer) Refresh()                     { r.Layout(r.chart.Size()) }
func (r *paceRenderer) Destroy()                     {}
func (r *paceRenderer) Objects() []fyne.CanvasObject { return r.objects }
