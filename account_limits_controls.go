package main

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
)

const modelLimitsStripHeight float32 = 77

type modelAccountLayout struct{}

func (modelAccountLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(120, 48) }
func (modelAccountLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(size)
}

type limitGaugeLayout struct {
	window accountLimitWindow
}

func newLimitGauge(w accountLimitWindow) fyne.CanvasObject {
	label := textLine(strings.ReplaceAll(w.label, " · ", " "), 11, colMuted)
	value := textLine(fmt.Sprintf("%.0f %%", w.remaining()), 11, colText)
	value.TextStyle = fyne.TextStyle{Bold: true}
	track := canvas.NewRectangle(color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xff})
	fill := canvas.NewRectangle(colAmber)
	track.CornerRadius, fill.CornerRadius = 2, 2
	if w.remaining() <= 20 {
		fill.FillColor, value.Color = colDanger, colDanger
	}
	return container.New(limitGaugeLayout{w}, label, track, fill, value, textLine("", 11, colMuted))
}

func (l limitGaugeLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(98, 38)
}
func (l limitGaugeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	label, value := objects[0].(*canvas.Text), objects[3].(*canvas.Text)
	width := fyne.Min(value.MinSize().Width, size.Width)
	value.Move(fyne.NewPos(size.Width-width, 0))
	labelWidth := fyne.Max(0, size.Width-width-6)
	label.Text = fitText(strings.ReplaceAll(l.window.label, " · ", " "), 11, fyne.TextStyle{}, labelWidth)
	label.Move(fyne.NewPos(0, 0))
	objects[1].Move(fyne.NewPos(0, 18))
	objects[1].Resize(fyne.NewSize(size.Width, 4))
	objects[2].Move(objects[1].Position())
	objects[2].Resize(fyne.NewSize(size.Width*float32(l.window.remaining())/100, 4))
	reset := objects[4].(*canvas.Text)
	text := "Remise non fournie"
	if !l.window.resetAt.IsZero() {
		text = "↻ " + l.window.resetAt.Local().Format("02/01 15:04")
	}
	reset.Text = fitText(text, 11, fyne.TextStyle{}, size.Width)
	reset.Move(fyne.NewPos(0, 24))
}

func modelLimitsStrip(c connection, limits accountLimits, problem string) fyne.CanvasObject {
	icon := canvas.NewImageFromResource(modelAccountBadge(c.Provider))
	icon.FillMode = canvas.ImageFillContain
	name := textLine(c.Name, 12, colText)
	name.TextStyle = fyne.TextStyle{Bold: true}
	tag := "· compte sélectionné"
	if problem != "" {
		tag = "· " + problem
	}
	at := ""
	if !limits.checked.IsZero() {
		at = "Relevé à " + limits.checked.Local().Format("15:04")
	}
	header := container.New(limitStripHeaderLayout{c.Name, tag}, icon, name, textLine(tag, 11, colMuted), textLine(at, 11, colMuted))
	var body fyne.CanvasObject = textLine(limits.note, 12, colMuted)
	if len(limits.windows) > 0 {
		cells := []fyne.CanvasObject{}
		for _, w := range limits.windows {
			cells = append(cells, newLimitGauge(w))
		}
		// Four windows fit at the reference width; extra windows remain
		// accessible by scrolling rather than squeezing their dates.
		body = container.NewHScroll(container.New(limitStripCellsLayout{}, cells...))
	}
	rule := canvas.NewRectangle(colBorderSoft)
	return container.New(limitStripLayout{}, rule, header, body)
}

type limitStripHeaderLayout struct{ name, tag string }

func (limitStripHeaderLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, 18) }
func (l limitStripHeaderLayout) Layout(o []fyne.CanvasObject, size fyne.Size) {
	o[0].Move(fyne.NewPos(0, 1))
	o[0].Resize(fyne.NewSquareSize(16))
	at := o[3].MinSize().Width
	o[3].Move(fyne.NewPos(size.Width-at, 1))
	name := o[1].(*canvas.Text)
	name.Text = fitText(l.name, 12, name.TextStyle, fyne.Max(0, size.Width-at-30))
	name.Move(fyne.NewPos(23, 0))
	x := 23 + name.MinSize().Width + 6
	tag := o[2].(*canvas.Text)
	tag.Text = fitText(l.tag, 11, fyne.TextStyle{}, fyne.Max(0, size.Width-at-x-8))
	tag.Move(fyne.NewPos(x, 1))
}

type limitStripCellsLayout struct{}

func (limitStripCellsLayout) MinSize(o []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(float32(len(o))*110-12, 38)
}
func (limitStripCellsLayout) Layout(o []fyne.CanvasObject, size fyne.Size) {
	for i, cell := range o {
		cell.Move(fyne.NewPos(float32(i)*110, 0))
		cell.Resize(fyne.NewSize(98, size.Height))
	}
}

type limitStripLayout struct{}

func (limitStripLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, modelLimitsStripHeight)
}
func (limitStripLayout) Layout(o []fyne.CanvasObject, size fyne.Size) {
	o[0].Move(fyne.NewPos(0, 0))
	o[0].Resize(fyne.NewSize(size.Width, 1))
	o[1].Move(fyne.NewPos(12, 7))
	o[1].Resize(fyne.NewSize(fyne.Max(0, size.Width-24), 18))
	o[2].Move(fyne.NewPos(12, 31))
	o[2].Resize(fyne.NewSize(fyne.Max(0, size.Width-24), 38))
}
