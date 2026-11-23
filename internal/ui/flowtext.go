// SPDX-License-Identifier: MPL-2.0

package ui

// flowText draws one block of styled text (a paragraph, a heading, a list
// item, a table cell): it wraps the spans the Markdown parser produced in the width
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
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/palette"
	"1loop/internal/platform"
)

var (
	colLink     = darkAccent.Link // the accent, a little softer for reading
	colCodeChip = palette.Dark.CodeChip
	colCodeEdge = palette.Dark.CodeEdge
	colCodeInk  = palette.Dark.CodeInk
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
	paintedWidth  float32 // differs from width after measuring hidden text
	measuring     bool    // the transcript needs height without new drawing objects
	natural       float32 // the width of the widest line as laid out
	widest        float32 // the widest thing that cannot be broken
	objects       []fyne.CanvasObject
	pieces        []selPiece
	links         []flowLink
	waiting       bool       // a formula is being typeset: flow again when it is ready
	cut           []atom     // the spans as measured words, kept from one width to the next
	marks         []lineMark // where each line laid out starts (grow)
	drawn         bool       // objects are those of the lines in marks
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
	case spanValue.cite:
		size, ink = flow.size*0.85, colMuted
	case spanValue.target() != "":
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
	size := len(flow.spans)
	for _, span := range flow.spans {
		size += len(span.text) / 3 // a word and a space for about every six bytes: grown once at most
	}
	return flow.cutAtoms(make([]atom, 0, size), 0, 0)
}

// cutAtoms cuts the spans from span from on, the first of them from its
// byte at (a plain span: grow), after the atoms out already holds.
func (flow *flowText) cutAtoms(out []atom, from, at int) []atom {
	add := func(text string, i int, space bool) *atom {
		style, size, ink := flow.look(flow.spans[i])
		atomValue := atom{text: text, span: i, style: style, size: size, ink: ink, space: space}
		atomValue.width, atomValue.height, atomValue.base = measureText(text, size, style)
		out = append(out, atomValue)
		return &out[len(out)-1]
	}
	flow.waiting = false
	for i := from; i < len(flow.spans); i++ {
		span := flow.spans[i]
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
			start, skip := -1, 0 // of the word being read, in bytes
			if i == from {
				skip = at
			}
			for at, r := range span.text[skip:] {
				at += skip
				if r == ' ' || r == '\t' || r == '\n' {
					if start >= 0 {
						add(span.text[start:at], i, false)
						start = -1
					}
					if n := len(out); n == 0 || !out[n-1].space {
						add(" ", i, true)
					}
					continue
				}
				if start < 0 {
					start = at
				}
			}
			if start >= 0 {
				add(span.text[start:], i, false)
			}
		}
		if (span.code || span.cite) && len(out) > first { // room for the chip around the code
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
	start int  // the index of the atom it starts from
	clean bool // it starts at that atom, not inside a word cut across lines
}

// atomBuffers hold the atoms of the lines being laid out (lines), from one
// layout to the next: a copy of every word at each layout was most of what
// opening a chat allocated.
var atomBuffers = sync.Pool{New: func() any { return new([]atom) }}

// lines breaks the atoms into lines no wider than width, their atoms in
// buf (from atomBuffers, given back once the lines are drawn). A line that
// starts clean is laid out the same from its first atom alone: grow
// starts there again.
func (flow *flowText) lines(atoms []atom, width float32, buf []atom) ([]flowLine, []atom) {
	var lines []flowLine
	buf = buf[:0]                    // the lines' atoms, one after the other
	from, begin, clean := 0, 0, true // the line being filled: in buf, in atoms
	x := float32(0)
	flush := func(hard bool, next int, nextClean bool) {
		for len(buf) > from && buf[len(buf)-1].space { // a line does not end with a space
			buf = buf[:len(buf)-1]
		}
		lines = append(lines, flowLine{buf[from:len(buf):len(buf)], hard, begin, clean})
		from, x, begin, clean = len(buf), 0, next, nextClean
	}
	for i, atomValue := range atoms {
		if atomValue.hard {
			flush(true, i+1, true)
			continue
		}
		if atomValue.space && len(buf) == from {
			if begin == i {
				begin++ // where the line really starts: grow compares with it
			}
			continue
		}
		if atomValue.image != nil && atomValue.width > width { // wider than the column: the whole formula, smaller
			scale := width / atomValue.width
			atomValue.width, atomValue.height, atomValue.base = atomValue.width*scale, atomValue.height*scale, atomValue.base*scale
		}
		if !flow.mono && !atomValue.space && x+atomValue.width > width && len(buf) > from {
			flush(false, i, true)
		}
		// A word wider than the line (a long address) is cut where it must.
		// Each character is measured once: measuring every remaining part
		// again cost the square of the word's length (a long base64 string
		// froze the window for seconds and allocated gigabytes).
		if !flow.mono && atomValue.image == nil && atomValue.width > width && utf8.RuneCountInString(atomValue.text) > 1 {
			runes := []rune(atomValue.text)
			advance := make([]float32, len(runes)+1) // advance[i]: the width of runes[i:]
			for i := len(runes) - 1; i >= 0; i-- {
				w, _, _ := measureText(string(runes[i]), atomValue.size, atomValue.style)
				advance[i] = advance[i+1] + w
			}
			start := 0
			for len(runes)-start > 1 && atomValue.lead+advance[start]+atomValue.trail > width {
				fit := 1
				for start+fit < len(runes) && atomValue.lead+advance[start]-advance[start+fit+1] <= width {
					fit++
				}
				head := atomValue
				head.text = string(runes[start : start+fit])
				head.width, _, _ = measureText(head.text, atomValue.size, atomValue.style)
				head.width += atomValue.lead
				head.trail = 0
				buf = append(buf, head)
				flush(false, i, false)
				start += fit
				atomValue.lead = 0
			}
			atomValue.text = string(runes[start:])
			atomValue.width, _, _ = measureText(atomValue.text, atomValue.size, atomValue.style)
			atomValue.width += atomValue.trail
		}
		buf = append(buf, atomValue)
		x += atomValue.width
	}
	if len(buf) > from || len(lines) == 0 {
		flush(true, len(atoms), true)
	}
	return lines, buf
}

// lineMark is where a laid-out line starts, and what came before it:
// enough for grow to lay the text out again from that line on.
type lineMark struct {
	start                  int  // its first atom (flowLine.start)
	clean                  bool // flowLine.clean
	y, natural             float32
	objects, pieces, links int // how many were drawn before it
}

// flow lays the block out for a width and rebuilds what it draws.
func (flow *flowText) flow(width float32) { flow.flowFrom(width, 0) }

// flowFrom is flow, keeping the lines before line from as they are laid
// out (grow): their atoms did not change, nor the width.
func (flow *flowText) flowFrom(width float32, from int) {
	if from <= 0 || from >= len(flow.marks) || !flow.marks[from].clean || width != flow.width || flow.cut == nil ||
		(!flow.measuring && !flow.drawn) {
		from = 0
	}
	flow.width = width
	if flow.cut == nil { // measuring every word is the costly part: once is enough
		flow.cut = flow.atoms()
	}
	atoms := flow.cut
	y, start := float32(0), 0
	if from > 0 {
		mark := flow.marks[from]
		y, start, flow.natural = mark.y, mark.start, mark.natural
		if !flow.measuring {
			clear(flow.objects[mark.objects:])
			flow.objects, flow.pieces, flow.links = flow.objects[:mark.objects], flow.pieces[:mark.pieces], flow.links[:mark.links]
		}
		flow.marks = flow.marks[:from]
	} else {
		flow.natural, flow.marks = 0, flow.marks[:0]
		if !flow.measuring {
			clear(flow.objects)
			flow.objects, flow.pieces, flow.links = flow.objects[:0], flow.pieces[:0], flow.links[:0]
		}
	}
	flow.widest = 0
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
	buffer := atomBuffers.Get().(*[]atom)
	var lines []flowLine
	lines, *buffer = flow.lines(atoms[start:], room, *buffer)
	defer func() {
		clear(*buffer) // its words and pictures are not kept alive by the pool
		atomBuffers.Put(buffer)
	}()
	// What each line draws, put in the objects line after line: chips
	// under their text, rules and pictures over it.
	var chips, marks, texts, pictures []fyne.CanvasObject
	if flow.marker != "" && !flow.measuring && from == 0 {
		mark := canvas.NewText(flow.marker, colMuted)
		mark.TextSize = flow.size
		w, h, _ := measureText(flow.marker, flow.size, fyne.TextStyle{})
		mark.Move(fyne.NewPos(fyne.Max(0, flow.hang-w-7), 0))
		mark.Resize(fyne.NewSize(w, h))
		flow.objects = append(flow.objects, mark)
		flow.pieces = append(flow.pieces, selPiece{runes: []rune(flow.marker + " "), at: mark.Position(), size: fyne.NewSize(w, h),
			textSize: flow.size, solid: true})
	}
	for n, line := range lines {
		flow.marks = append(flow.marks, lineMark{start + line.start, line.clean, y, flow.natural, len(flow.objects), len(flow.pieces), len(flow.links)})
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
		if flow.measuring {
			y += height
			if n < len(lines)-1 {
				y += leading
			}
			continue
		}
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
				if span.code || span.cite || (span.math != "" && first.image == nil) {
					chip := canvas.NewRectangle(colCodeChip)
					chip.CornerRadius = rounded(4)
					chip.StrokeColor, chip.StrokeWidth = colCodeEdge, 1
					chip.Move(fyne.NewPos(x, top-1))
					chip.Resize(fyne.NewSize(runWidth, first.height+2))
					chips = append(chips, chip)
				}
				if link := span.target(); link != "" {
					if !span.cite { // a citation is a chip, not underlined
						rule := canvas.NewRectangle(withAlpha(colLink, 0x70))
						rule.Move(fyne.NewPos(at.X, top+first.base+2))
						rule.Resize(fyne.NewSize(textWidth, 1))
						marks = append(marks, rule)
					}
					flow.links = append(flow.links, flowLink{fyne.NewPos(x, top), size, link})
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
		flow.objects = append(append(append(append(flow.objects, chips...), texts...), marks...), pictures...)
		chips, texts, marks, pictures = chips[:0], texts[:0], marks[:0], pictures[:0]
		y += height
		if n < len(lines)-1 {
			y += leading
		}
	}
	if flow.mono && len(lines) > 0 {
		y += leading // code scrolls sideways, clipped at its height: room for the underscores of its last line
	}
	flow.height = y
	if flow.measuring {
		flow.drawn = false // the objects, if any, are of an earlier layout
	} else {
		flow.paintedWidth, flow.drawn = width, true
	}
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

// release drops what the block keeps to draw itself (its measured words,
// drawing objects, selection pieces): it is out of sight, only its height
// counts. They are made again when it is laid out on screen.
func (flow *flowText) release() {
	if flow.waiting {
		return // a formula is coming: flow is called again then
	}
	flow.cut, flow.objects, flow.pieces, flow.links, flow.marks = nil, nil, nil, nil, nil
	flow.paintedWidth, flow.drawn = 0, false
}

// grow gives the block new spans. While an answer streams they are the
// ones it has, the last one longer, perhaps with more after it: the words
// already measured stay, and so do the lines before the one being written,
// with what they draw. Only the rest is cut and laid out again, so a
// delta costs what it adds, not the length of the paragraph.
func (flow *flowText) grow(spans []span) {
	keep, from, at := flow.kept(spans)
	flow.spans = spans
	if keep < 0 {
		flow.cut = nil
	} else {
		flow.cut = flow.cutAtoms(flow.cut[:keep], from, at)
	}
	if flow.width == 0 {
		return // never laid out: MinSize and Layout do it
	}
	line := 0
	for i := len(flow.marks) - 1; keep > 0 && i > 0; i-- {
		if flow.marks[i].start < keep && flow.marks[i].clean { // the line holding the last atom kept
			line = i
			break
		}
	}
	// Out of sight (released, or never drawn), it is only measured.
	painted, measuring := flow.paintedWidth != 0, flow.measuring
	flow.measuring = measuring || !painted
	flow.flowFrom(flow.width, line)
	flow.measuring = measuring
	if measuring || !painted {
		flow.paintedWidth = 0 // what it draws is of the shorter text: drawn again when shown
	} else if c := fyne.CurrentApp().Driver().CanvasForObject(flow); c != nil {
		// The new lines are drawn at the next frame. canvas.Refresh would
		// free the textures of every line, the unchanged ones too.
		setDirty(c)
	}
}

// kept tells how much of what the block measured still holds for spans:
// the atoms before keep, and the rest is cut from span from, at its byte
// at. The spans before the first one that changed hold, and that one too
// when it only got longer (plain text being written); markup that closes
// (**bo, then **bold**) changes the spans from there on. keep is -1 when
// nothing holds.
func (flow *flowText) kept(spans []span) (keep, from, at int) {
	old := flow.spans
	if flow.cut == nil || flow.mono || flow.waiting {
		return -1, 0, 0
	}
	from = 0
	for from < len(old) && from < len(spans) && spans[from] == old[from] {
		from++
	}
	// The atoms are in the order of their spans: those of the spans kept.
	keep = len(flow.cut)
	for keep > 0 && flow.cut[keep-1].span >= from {
		keep--
	}
	if from == len(old) || from == len(spans) {
		return keep, from, 0
	}
	was, now := old[from], spans[from]
	grown := now
	grown.text = was.text
	if grown != was || !strings.HasPrefix(now.text, was.text) || was.hard || was.code || was.cite || was.math != "" {
		return keep, from, 0 // cut again from its start
	}
	// The words of the span that got longer, but its last: it may go on.
	end := keep
	for end < len(flow.cut) && flow.cut[end].span == from {
		end++
	}
	at = len(was.text)
	if n := len(was.text); n > 0 && !strings.ContainsRune(" \t\n", rune(was.text[n-1])) {
		if end == keep || flow.cut[end-1].space {
			return keep, from, 0
		}
		end--
		at -= len(flow.cut[end].text)
	}
	return end, from, at
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
	if strings.HasPrefix(address, citeScheme) {
		openCitation(address)
		return
	}
	if _, ok := referenceOf(address); ok { // an issue or pull request: the side panel shows it
		openReference(address)
		return
	}
	openInBrowser(address)
}

// openInBrowser opens a web or mail address with the desktop's
// application, an issue or pull request too (an image, "Open on GitHub":
// through openLink, it showed in the side panel again, #441).
func openInBrowser(address string) {
	target, err := url.Parse(address)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https" && target.Scheme != "mailto") {
		platform.Devf("ui", "link not opened (not a web or mail address): %s", address)
		return
	}
	platform.Devf("ui", "open link %s", address)
	if err := openURL(target); err != nil {
		platform.Devf("ui", "could not open %s: %v", address, err)
	}
}

// openURL hands an address to the desktop (replaced in tests).
var openURL = func(target *url.URL) error { return fyne.CurrentApp().OpenURL(target) }

func (flow *flowText) CreateRenderer() fyne.WidgetRenderer {
	return &flowRenderer{flow}
}

type flowRenderer struct{ flowText *flowText }

// MinSize: the height is that of the last width laid out, like any wrapped
// text; before any layout, that of a column of the conversation.
func (renderer *flowRenderer) MinSize() fyne.Size {
	text := renderer.flowText
	if text.width == 0 {
		// Keep this provisional layout: repeated MinSize calls must not
		// rebuild it. Layout reflows the text when its actual width differs,
		// so it only measures: what it would draw is thrown away at once.
		measuring := text.measuring
		text.measuring = true
		text.flow(600)
		text.measuring = measuring
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
	if size.Width > 0 && (size.Width != renderer.flowText.width || (!renderer.flowText.measuring && size.Width != renderer.flowText.paintedWidth)) {
		renderer.flowText.flow(size.Width)
	}
}

func (renderer *flowRenderer) Refresh() {
	if !renderer.flowText.measuring && renderer.flowText.width != renderer.flowText.paintedWidth {
		renderer.flowText.flow(renderer.flowText.width)
	}
	for _, object := range renderer.flowText.objects {
		object.Refresh()
	}
	canvas.Refresh(renderer.flowText)
}

func (renderer *flowRenderer) Objects() []fyne.CanvasObject { return renderer.flowText.objects }
func (renderer *flowRenderer) Destroy()                     {}
