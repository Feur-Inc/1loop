package main

// flowText draws one block of styled text (a paragraph, a heading, a list
// item, a table cell): it wraps the spans parseInline produced in the width
// it is given, and draws what Fyne's rich text does not: inline code on a
// tinted chip, links that look and act like links, struck text, typeset
// formulas sitting on the text's baseline.
//
// Since it lays every run of text out itself, it also knows where each one
// is: that is what text selection (selection.go) and the links work from.

import (
	"image/color"
	"net/url"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

var (
	colLink     = color.NRGBA{R: 0xff, G: 0xc2, B: 0x3d, A: 0xff} // the accent, a little softer for reading
	colCodeChip = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x14}
	colCodeEdge = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12}
	colCodeInk  = color.NRGBA{R: 0xea, G: 0xea, B: 0xea, A: 0xff}
)

const (
	flowChipPad   = 4    // room on each side of inline code, inside its chip
	flowLeading   = 0.34 // space between two lines, as a share of the text size
	flowCodeScale = 0.93 // monospace reads larger than the text face at the same size
)

type flowText struct {
	widget.BaseWidget
	spans  []span
	size   float32
	ink    color.Color
	bold   bool    // a heading: everything is bold
	mono   bool    // code: nothing wraps, spaces count
	center bool    // a formula on its own line
	marker string  // a list item's bullet or number, drawn in the margin
	hang   float32 // that margin
	tail   string  // what follows the block in copied text

	width, height float32
	natural       float32 // the width of the widest line as laid out
	widest        float32 // the widest thing that cannot be broken
	objects       []fyne.CanvasObject
	pieces        []selPiece
	links         []flowLink
	waiting       bool   // a formula is being typeset: flow again when it is ready
	cut           []atom // the spans as measured words, kept from one width to the next
}

type flowLink struct {
	at   fyne.Position
	size fyne.Size
	url  string
}

func newFlowText(spans []span, size float32, ink color.Color) *flowText {
	f := &flowText{spans: spans, size: size, ink: ink, tail: "\n\n"}
	f.ExtendBaseWidget(f)
	return f
}

// atom is the smallest thing a line is made of: a word, a space, a formula.
type atom struct {
	text   string
	span   int // index of its span
	style  fyne.TextStyle
	size   float32
	ink    color.Color
	width  float32
	height float32
	base   float32 // from its top down to the baseline
	lead   float32 // empty room before the text (a code chip's padding)
	space  bool
	hard   bool
	image  *mathPicture
}

func (f *flowText) look(s span) (fyne.TextStyle, float32, color.Color) {
	style := fyne.TextStyle{Bold: s.bold || f.bold, Italic: s.italic, Monospace: f.mono}
	size, ink := f.size, f.ink
	switch {
	case s.code:
		style.Monospace = true
		size, ink = f.size*flowCodeScale, colCodeInk
	case s.link != "":
		ink = colLink
	}
	return style, size, ink
}

func measureText(text string, size float32, style fyne.TextStyle) (width, height, base float32) {
	bounds, baseline := fyne.CurrentApp().Driver().RenderedTextSize(text, size, style, nil)
	return bounds.Width, bounds.Height, baseline
}

// atoms cuts the spans into words and spaces, measured.
func (f *flowText) atoms() []atom {
	var out []atom
	add := func(text string, i int, space bool) *atom {
		style, size, ink := f.look(f.spans[i])
		a := atom{text: text, span: i, style: style, size: size, ink: ink, space: space}
		a.width, a.height, a.base = measureText(text, size, style)
		out = append(out, a)
		return &out[len(out)-1]
	}
	f.waiting = false
	for i, s := range f.spans {
		switch {
		case s.hard:
			out = append(out, atom{hard: true, span: i})
			continue
		case s.math != "":
			picture, pending := typeset(s.math, f.center, f.size)
			if picture != nil {
				out = append(out, atom{text: s.text, span: i, image: picture, width: picture.width,
					height: picture.height, base: picture.height - picture.depth})
				continue
			}
			f.waiting = f.waiting || pending
			// No TeX here, or not ready yet: the source, set apart like code.
			f.spans[i].code = true
			first := len(out)
			for n, word := range strings.Fields(s.text) {
				if n > 0 {
					add(" ", i, true)
				}
				add(word, i, false)
			}
			f.spans[i].code = false
			if len(out) > first {
				out[first].lead = flowChipPad
				out[first].width += flowChipPad
				out[len(out)-1].width += flowChipPad
			}
			continue
		}
		first := len(out)
		if f.mono {
			add(strings.ReplaceAll(s.text, "\t", "    "), i, false)
		} else {
			word := ""
			for _, r := range s.text {
				if r == ' ' || r == '\t' || r == '\n' {
					if word != "" {
						add(word, i, false)
						word = ""
					}
					if n := len(out); n == 0 || !out[n-1].space {
						add(" ", i, true)
					}
					continue
				}
				word += string(r)
			}
			if word != "" {
				add(word, i, false)
			}
		}
		if s.code && len(out) > first { // room for the chip around the code
			out[first].lead = flowChipPad
			out[first].width += flowChipPad
			out[len(out)-1].width += flowChipPad
		}
	}
	return out
}

type flowLine struct {
	atoms []atom
	hard  bool // ended by a line break, not by the width
}

// lines breaks the atoms into lines no wider than width.
func (f *flowText) lines(atoms []atom, width float32) []flowLine {
	var lines []flowLine
	var line []atom
	x := float32(0)
	flush := func(hard bool) {
		for len(line) > 0 && line[len(line)-1].space { // a line does not end with a space
			line = line[:len(line)-1]
		}
		lines = append(lines, flowLine{line, hard})
		line, x = nil, 0
	}
	for _, a := range atoms {
		if a.hard {
			flush(true)
			continue
		}
		if a.space && len(line) == 0 {
			continue
		}
		if a.image != nil && a.width > width { // wider than the column: the whole formula, smaller
			scale := width / a.width
			a.width, a.height, a.base = a.width*scale, a.height*scale, a.base*scale
		}
		if !f.mono && !a.space && x+a.width > width && len(line) > 0 {
			flush(false)
		}
		// A word wider than the line (a long address) is cut where it must.
		for !f.mono && a.image == nil && a.width > width && len([]rune(a.text)) > 1 {
			runes := []rune(a.text)
			fit := 1
			for fit < len(runes) {
				if w, _, _ := measureText(string(runes[:fit+1]), a.size, a.style); a.lead+w > width {
					break
				}
				fit++
			}
			head := a
			head.text = string(runes[:fit])
			head.width, _, _ = measureText(head.text, a.size, a.style)
			head.width += a.lead
			line = append(line, head)
			flush(false)
			a.text, a.lead = string(runes[fit:]), 0
			a.width, _, _ = measureText(a.text, a.size, a.style)
		}
		line = append(line, a)
		x += a.width
	}
	if len(line) > 0 || len(lines) == 0 {
		flush(true)
	}
	return lines
}

// flow lays the block out for a width and rebuilds what it draws.
func (f *flowText) flow(width float32) {
	f.width = width
	f.objects, f.pieces, f.links = f.objects[:0], f.pieces[:0], f.links[:0]
	if f.cut == nil { // measuring every word is the costly part: once is enough
		f.cut = f.atoms()
	}
	atoms := f.cut
	f.natural, f.widest = 0, 0
	for _, a := range atoms {
		if !a.space {
			f.widest = fyne.Max(f.widest, a.width)
		}
	}
	_, plainHeight, plainBase := measureText("Ag", f.size, fyne.TextStyle{Bold: f.bold, Monospace: f.mono})
	leading := float32(int(f.size*flowLeading + 0.5))
	if f.mono {
		leading = 3
	}
	room := fyne.Max(width-f.hang, 40)
	lines := f.lines(atoms, room)
	var chips, marks, texts, pictures []fyne.CanvasObject
	if f.marker != "" {
		mark := canvas.NewText(f.marker, colMuted)
		mark.TextSize = f.size
		w, h, _ := measureText(f.marker, f.size, fyne.TextStyle{})
		mark.Move(fyne.NewPos(fyne.Max(0, f.hang-w-7), 0))
		mark.Resize(fyne.NewSize(w, h))
		texts = append(texts, mark)
		f.pieces = append(f.pieces, selPiece{runes: []rune(f.marker + " "), at: mark.Position(), size: fyne.NewSize(w, h),
			textSize: f.size, solid: true})
	}
	y := float32(0)
	for n, line := range lines {
		height, base, lineWidth := plainHeight, plainBase, float32(0)
		for _, a := range line.atoms {
			lineWidth += a.width
			if a.base > base {
				height += a.base - base
				base = a.base
			}
		}
		for _, a := range line.atoms {
			if below := base + a.height - a.base; below > height {
				height = below
			}
		}
		f.natural = fyne.Max(f.natural, f.hang+lineWidth)
		x := f.hang
		if f.center {
			x = fyne.Max(0, (width-lineWidth)/2)
		}
		// Atoms of one span that follow each other on the line are one run.
		for i := 0; i < len(line.atoms); {
			first := line.atoms[i]
			j := i + 1
			if first.image == nil {
				for j < len(line.atoms) && line.atoms[j].span == first.span && line.atoms[j].image == nil {
					j++
				}
			}
			runWidth, text := float32(0), ""
			for _, a := range line.atoms[i:j] {
				runWidth += a.width
				text += a.text
			}
			s := f.spans[first.span]
			top := y + base - first.base
			at := fyne.NewPos(x+first.lead, top)
			size := fyne.NewSize(runWidth, first.height)
			last := j == len(line.atoms)
			after := ""
			if last {
				switch {
				case n == len(lines)-1:
					after = f.tail
				case line.hard:
					after = "\n"
				default:
					after = " "
				}
			}
			switch {
			case first.image != nil:
				picture := canvas.NewImageFromImage(first.image.image)
				picture.FillMode = canvas.ImageFillStretch
				picture.ScaleMode = canvas.ImageScaleSmooth
				picture.Move(fyne.NewPos(x, top))
				picture.Resize(size)
				pictures = append(pictures, picture)
				f.pieces = append(f.pieces, selPiece{runes: []rune(first.text), at: fyne.NewPos(x, top), size: size, solid: true, after: after})
			default:
				textWidth := float32(0)
				if text != "" { // an empty line of code draws nothing but still counts
					drawn := canvas.NewText(text, first.ink)
					drawn.TextSize, drawn.TextStyle = first.size, first.style
					drawn.Move(at)
					textWidth, _, _ = measureText(text, first.size, first.style)
					drawn.Resize(fyne.NewSize(textWidth, first.height))
					texts = append(texts, drawn)
				}
				f.pieces = append(f.pieces, selPiece{runes: []rune(text), at: at, size: fyne.NewSize(textWidth, first.height),
					textSize: first.size, style: first.style, after: after})
				if s.code || (s.math != "" && first.image == nil) {
					chip := canvas.NewRectangle(colCodeChip)
					chip.CornerRadius = 4
					chip.StrokeColor, chip.StrokeWidth = colCodeEdge, 1
					chip.Move(fyne.NewPos(x, top-1))
					chip.Resize(fyne.NewSize(runWidth, first.height+2))
					chips = append(chips, chip)
				}
				if s.link != "" {
					rule := canvas.NewRectangle(withAlpha(colLink, 0x70))
					rule.Move(fyne.NewPos(at.X, top+first.base+2))
					rule.Resize(fyne.NewSize(textWidth, 1))
					marks = append(marks, rule)
					f.links = append(f.links, flowLink{fyne.NewPos(x, top), size, s.link})
				}
				if s.strike {
					rule := canvas.NewRectangle(first.ink)
					rule.Move(fyne.NewPos(at.X, top+first.base*0.68))
					rule.Resize(fyne.NewSize(textWidth, 1))
					marks = append(marks, rule)
				}
			}
			x += runWidth
			i = j
		}
		y += height
		if n < len(lines)-1 {
			y += leading
		}
	}
	f.height = y
	f.objects = append(append(append(append(f.objects, chips...), texts...), marks...), pictures...)
	if f.waiting {
		whenTypeset(func() {
			if f.waiting {
				f.cut = nil
				f.flow(f.width)
				f.Refresh()
			}
		})
	}
}

// selPieces gives the selection layer what is drawn and where.
func (f *flowText) selPieces() []selPiece { return f.pieces }

// linkAt is the address of the link at a point of the block, if any.
func (f *flowText) linkAt(at fyne.Position) string {
	for _, link := range f.links {
		if at.X >= link.at.X && at.Y >= link.at.Y && at.X < link.at.X+link.size.Width && at.Y < link.at.Y+link.size.Height {
			return link.url
		}
	}
	return ""
}

// Tapped opens the link under the pointer.
func (f *flowText) Tapped(e *fyne.PointEvent) {
	openLink(f.linkAt(e.Position))
}

// openLink opens a web or mail address with the desktop's application.
func openLink(address string) {
	if address == "" {
		return
	}
	target, err := url.Parse(address)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https" && target.Scheme != "mailto") {
		devf("ui", "link not opened (not a web or mail address): %s", address)
		return
	}
	devf("ui", "open link %s", address)
	if err := fyne.CurrentApp().OpenURL(target); err != nil {
		devf("ui", "could not open it: %v", err)
	}
}

func (f *flowText) CreateRenderer() fyne.WidgetRenderer {
	return &flowRenderer{f}
}

type flowRenderer struct{ f *flowText }

// MinSize: the height is that of the last width laid out, like any wrapped
// text; before any layout, that of a column of the conversation.
func (r *flowRenderer) MinSize() fyne.Size {
	f := r.f
	if f.width == 0 {
		f.flow(600)
		f.width = 0 // still to be laid out for real
	}
	if f.mono {
		return fyne.NewSize(f.natural, f.height) // code is never wrapped: it scrolls sideways
	}
	return fyne.NewSize(40, f.height)
}

// extent measures the block when nothing limits its width: how wide it
// then is, and its widest word. Tables size their columns with it.
func (f *flowText) extent() (natural, widest float32) {
	f.flow(100000)
	natural, widest = f.natural, f.widest
	f.width = 0
	return natural, widest
}

func (r *flowRenderer) Layout(size fyne.Size) {
	if size.Width != r.f.width && size.Width > 0 {
		r.f.flow(size.Width)
	}
}

func (r *flowRenderer) Refresh() {
	for _, o := range r.f.objects {
		o.Refresh()
	}
	canvas.Refresh(r.f)
}

func (r *flowRenderer) Objects() []fyne.CanvasObject { return r.f.objects }
func (r *flowRenderer) Destroy()                     {}
