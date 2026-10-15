package ui

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
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/platform"
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
	tight  bool    // lines as close as a Fyne label's (user messages)
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
	text := &flowText{spans: spans, size: size, ink: ink, tail: "\n\n"}
	text.ExtendBaseWidget(text)
	return text
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
	trail  float32 // and after it
	space  bool
	hard   bool
	image  *mathPicture
}

func (flow *flowText) look(spanValue span) (fyne.TextStyle, float32, color.Color) {
	style := fyne.TextStyle{Bold: spanValue.bold || flow.bold, Italic: spanValue.italic, Monospace: flow.mono}
	size, ink := flow.size, flow.ink
	switch {
	case spanValue.code:
		style.Monospace = true
		size, ink = flow.size*flowCodeScale, colCodeInk
	case spanValue.link != "":
		ink = colLink
	}
	return style, size, ink
}

type measureKey struct {
	text  string
	size  float32
	style fyne.TextStyle
}

type measured struct{ width, height, base float32 }

// measures keeps the size of every word drawn: shaping one is the costly
// part of opening a chat, and the same words come back all the time.
var (
	measuresMu sync.Mutex
	measures   = map[measureKey]measured{}
)

// textSize is fyne.MeasureText through the same cache. Fyne's own cache
// reads the clock at every lookup, which costs microseconds on machines
// that time with HPET, and buttons measure their text at every frame.
func textSize(text string, size float32, style fyne.TextStyle) fyne.Size {
	width, height, _ := measureText(text, size, style)
	return fyne.NewSize(width, height)
}

func measureText(text string, size float32, style fyne.TextStyle) (width, height, base float32) {
	key := measureKey{text, size, style}
	measuresMu.Lock()
	measuredValue, ok := measures[key]
	measuresMu.Unlock()
	if !ok {
		bounds, baseline := fyne.CurrentApp().Driver().RenderedTextSize(text, size, style, nil)
		measuredValue = measured{bounds.Width, bounds.Height, baseline}
		measuresMu.Lock()
		if len(measures) > 200000 {
			clear(measures)
		}
		measures[key] = measuredValue
		measuresMu.Unlock()
	}
	return measuredValue.width, measuredValue.height, measuredValue.base
}

// atoms cuts the spans into words and spaces, measured.
func (flow *flowText) atoms() []atom {
	var out []atom
	add := func(text string, i int, space bool) *atom {
		style, size, ink := flow.look(flow.spans[i])
		atomValue := atom{text: text, span: i, style: style, size: size, ink: ink, space: space}
		atomValue.width, atomValue.height, atomValue.base = measureText(text, size, style)
		out = append(out, atomValue)
		return &out[len(out)-1]
	}
	flow.waiting = false
	for i, span := range flow.spans {
		switch {
		case span.hard:
			out = append(out, atom{hard: true, span: i})
			continue
		case span.math != "":
			picture, pending := typeset(span.math, flow.center, flow.size)
			if picture != nil {
				out = append(out, atom{text: span.text, span: i, image: picture, width: picture.width,
					height: picture.height, base: picture.height - picture.depth})
				continue
			}
			flow.waiting = flow.waiting || pending
			// No TeX here, or not ready yet: the source, set apart like code.
			flow.spans[i].code = true
			first := len(out)
			for n, word := range strings.Fields(span.text) {
				if n > 0 {
					add(" ", i, true)
				}
				add(word, i, false)
			}
			flow.spans[i].code = false
			if len(out) > first {
				out[first].lead = flowChipPad
				out[first].width += flowChipPad
				out[len(out)-1].width += flowChipPad
				out[len(out)-1].trail = flowChipPad
				out[len(out)-1].trail = flowChipPad
			}
			continue
		}
		first := len(out)
		if flow.mono {
			add(strings.ReplaceAll(span.text, "\t", "    "), i, false)
		} else {
			word := ""
			for _, r := range span.text {
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
		if span.code && len(out) > first { // room for the chip around the code
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
func (flow *flowText) lines(atoms []atom, width float32) []flowLine {
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
	for _, atomValue := range atoms {
		if atomValue.hard {
			flush(true)
			continue
		}
		if atomValue.space && len(line) == 0 {
			continue
		}
		if atomValue.image != nil && atomValue.width > width { // wider than the column: the whole formula, smaller
			scale := width / atomValue.width
			atomValue.width, atomValue.height, atomValue.base = atomValue.width*scale, atomValue.height*scale, atomValue.base*scale
		}
		if !flow.mono && !atomValue.space && x+atomValue.width > width && len(line) > 0 {
			flush(false)
		}
		// A word wider than the line (a long address) is cut where it must.
		for !flow.mono && atomValue.image == nil && atomValue.width > width && len([]rune(atomValue.text)) > 1 {
			runes := []rune(atomValue.text)
			fit := 1
			for fit < len(runes) {
				if w, _, _ := measureText(string(runes[:fit+1]), atomValue.size, atomValue.style); atomValue.lead+w > width {
					break
				}
				fit++
			}
			head := atomValue
			head.text = string(runes[:fit])
			head.width, _, _ = measureText(head.text, atomValue.size, atomValue.style)
			head.width += atomValue.lead
			head.trail = 0
			line = append(line, head)
			flush(false)
			atomValue.text, atomValue.lead = string(runes[fit:]), 0
			atomValue.width, _, _ = measureText(atomValue.text, atomValue.size, atomValue.style)
			atomValue.width += atomValue.trail
		}
		line = append(line, atomValue)
		x += atomValue.width
	}
	if len(line) > 0 || len(lines) == 0 {
		flush(true)
	}
	return lines
}

// flow lays the block out for a width and rebuilds what it draws.
func (flow *flowText) flow(width float32) {
	flow.width = width
	flow.objects, flow.pieces, flow.links = flow.objects[:0], flow.pieces[:0], flow.links[:0]
	if flow.cut == nil { // measuring every word is the costly part: once is enough
		flow.cut = flow.atoms()
	}
	atoms := flow.cut
	flow.natural, flow.widest = 0, 0
	for _, atom := range atoms {
		if !atom.space {
			flow.widest = fyne.Max(flow.widest, atom.width)
		}
	}
	_, plainHeight, plainBase := measureText("Ag", flow.size, fyne.TextStyle{Bold: flow.bold, Monospace: flow.mono})
	leading := float32(int(flow.size*flowLeading + 0.5))
	if flow.mono {
		leading = 3
	}
	if flow.tight {
		leading = 0
	}
	room := fyne.Max(width-flow.hang, 40)
	lines := flow.lines(atoms, room)
	var chips, marks, texts, pictures []fyne.CanvasObject
	if flow.marker != "" {
		mark := canvas.NewText(flow.marker, colMuted)
		mark.TextSize = flow.size
		w, h, _ := measureText(flow.marker, flow.size, fyne.TextStyle{})
		mark.Move(fyne.NewPos(fyne.Max(0, flow.hang-w-7), 0))
		mark.Resize(fyne.NewSize(w, h))
		texts = append(texts, mark)
		flow.pieces = append(flow.pieces, selPiece{runes: []rune(flow.marker + " "), at: mark.Position(), size: fyne.NewSize(w, h),
			textSize: flow.size, solid: true})
	}
	y := float32(0)
	for n, line := range lines {
		height, base, lineWidth := plainHeight, plainBase, float32(0)
		for _, atomValue := range line.atoms {
			lineWidth += atomValue.width
			if atomValue.base > base {
				height += atomValue.base - base
				base = atomValue.base
			}
		}
		for _, a := range line.atoms {
			if below := base + a.height - a.base; below > height {
				height = below
			}
		}
		flow.natural = fyne.Max(flow.natural, flow.hang+lineWidth)
		x := flow.hang
		if flow.center {
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
			span := flow.spans[first.span]
			top := y + base - first.base
			at := fyne.NewPos(x+first.lead, top)
			size := fyne.NewSize(runWidth, first.height)
			last := j == len(line.atoms)
			after := ""
			if last {
				switch {
				case n == len(lines)-1:
					after = flow.tail
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
				flow.pieces = append(flow.pieces, selPiece{runes: []rune(first.text), at: fyne.NewPos(x, top), size: size, solid: true, after: after})
			default:
				textWidth := float32(0)
				if text != "" { // an empty line of code draws nothing but still counts
					drawn := canvas.NewText(text, first.ink)
					drawn.TextSize, drawn.TextStyle = first.size, first.style
					drawn.Move(at)
					// Its words are measured already: measuring the whole run
					// again, at every width laid out, was most of the cost.
					textWidth = runWidth - first.lead - line.atoms[j-1].trail
					drawn.Resize(fyne.NewSize(textWidth, first.height))
					texts = append(texts, drawn)
				}
				flow.pieces = append(flow.pieces, selPiece{runes: []rune(text), at: at, size: fyne.NewSize(textWidth, first.height),
					textSize: first.size, style: first.style, after: after})
				if span.code || (span.math != "" && first.image == nil) {
					chip := canvas.NewRectangle(colCodeChip)
					chip.CornerRadius = 4
					chip.StrokeColor, chip.StrokeWidth = colCodeEdge, 1
					chip.Move(fyne.NewPos(x, top-1))
					chip.Resize(fyne.NewSize(runWidth, first.height+2))
					chips = append(chips, chip)
				}
				if span.link != "" {
					rule := canvas.NewRectangle(withAlpha(colLink, 0x70))
					rule.Move(fyne.NewPos(at.X, top+first.base+2))
					rule.Resize(fyne.NewSize(textWidth, 1))
					marks = append(marks, rule)
					flow.links = append(flow.links, flowLink{fyne.NewPos(x, top), size, span.link})
				}
				if span.strike {
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
	flow.height = y
	flow.objects = append(append(append(append(flow.objects, chips...), texts...), marks...), pictures...)
	if flow.waiting {
		whenTypeset(func() {
			if flow.waiting {
				flow.cut = nil
				flow.flow(flow.width)
				flow.Refresh()
			}
		})
	}
}

// selPieces gives the selection layer what is drawn and where.
func (flow *flowText) selPieces() []selPiece { return flow.pieces }

// linkAt is the address of the link at a point of the block, if any.
func (flow *flowText) linkAt(at fyne.Position) string {
	for _, link := range flow.links {
		if at.X >= link.at.X && at.Y >= link.at.Y && at.X < link.at.X+link.size.Width && at.Y < link.at.Y+link.size.Height {
			return link.url
		}
	}
	return ""
}

// Tapped opens the link under the pointer.
func (flow *flowText) Tapped(e *fyne.PointEvent) {
	openLink(flow.linkAt(e.Position))
}

// openLink opens a web or mail address with the desktop's application.
func openLink(address string) {
	if address == "" {
		return
	}
	target, err := url.Parse(address)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https" && target.Scheme != "mailto") {
		platform.Devf("ui", "link not opened (not a web or mail address): %s", address)
		return
	}
	platform.Devf("ui", "open link %s", address)
	if err := fyne.CurrentApp().OpenURL(target); err != nil {
		platform.Devf("ui", "could not open it: %v", err)
	}
}

func (flow *flowText) CreateRenderer() fyne.WidgetRenderer {
	return &flowRenderer{flow}
}

type flowRenderer struct{ flowText *flowText }

// MinSize: the height is that of the last width laid out, like any wrapped
// text; before any layout, that of a column of the conversation.
func (renderer *flowRenderer) MinSize() fyne.Size {
	text := renderer.flowText
	if text.width == 0 {
		text.flow(600)
		text.width = 0 // still to be laid out for real
	}
	if text.mono {
		return fyne.NewSize(text.natural, text.height) // code is never wrapped: it scrolls sideways
	}
	return fyne.NewSize(40, text.height)
}

// extent measures the block when nothing limits its width: how wide it
// then is, and its widest word. Tables size their columns with it.
func (flow *flowText) extent() (natural, widest float32) {
	flow.flow(100000)
	natural, widest = flow.natural, flow.widest
	flow.width = 0
	return natural, widest
}

func (renderer *flowRenderer) Layout(size fyne.Size) {
	if size.Width != renderer.flowText.width && size.Width > 0 {
		renderer.flowText.flow(size.Width)
	}
}

func (renderer *flowRenderer) Refresh() {
	for _, object := range renderer.flowText.objects {
		object.Refresh()
	}
	canvas.Refresh(renderer.flowText)
}

func (renderer *flowRenderer) Objects() []fyne.CanvasObject { return renderer.flowText.objects }
func (renderer *flowRenderer) Destroy()                     {}
