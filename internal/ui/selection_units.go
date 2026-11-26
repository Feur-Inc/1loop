// SPDX-License-Identifier: MPL-2.0

package ui

// The units a selection grows by, as in a browser or a text editor: one
// click places the caret, a double click takes the word under the pointer,
// a triple click its paragraph, and a drag that follows extends by the same
// unit. Everything here works on a layer's text as it is copied (selText):
// no geometry, so it is tested on its own (selection_units_test.go).

import (
	"strings"
	"time"
	"unicode"

	"fyne.io/fyne/v2"
)

// selText is the text of a layer's pieces in reading order, each followed by
// what separates it from the next (nothing, a space where a line wraps, a
// line break, a tab between table cells), and where each piece starts in it.
// A rewrap does not change it: a line wrapped at a space gives a space, one
// cut inside a long word gives nothing.
type selText struct {
	runes  []rune
	starts []int
	ends   []int  // where each piece's own runes end, its separator after
	object []bool // runes drawn as one thing (a formula, a list mark): never cut
}

func textOf(pieces []selPiece) selText {
	var text selText
	for i, piece := range pieces {
		text.starts = append(text.starts, len(text.runes))
		text.runes = append(text.runes, piece.runes...)
		for range piece.runes {
			text.object = append(text.object, piece.solid)
		}
		text.ends = append(text.ends, len(text.runes))
		if i < len(pieces)-1 {
			for _, r := range piece.after {
				text.runes = append(text.runes, r)
				text.object = append(text.object, false)
			}
		}
	}
	return text
}

// at is where a point is in the text. A point at the end of a piece, when a
// selection starts there, is the start of the next one: what separates them
// is not copied.
func (text selText) at(point selPoint, start bool) int {
	if point.index < 0 || point.index >= len(text.starts) {
		return 0
	}
	at := min(text.starts[point.index]+point.offset, text.ends[point.index])
	if start && at == text.ends[point.index] && at > text.starts[point.index] && point.index+1 < len(text.starts) {
		return text.starts[point.index+1]
	}
	return at
}

// point is the place of an offset among the pieces. An offset in a
// separator is the end of the piece before it for the end of a selection,
// the start of the piece after it for its start.
func (text selText) point(at int, end bool) selPoint {
	n := len(text.starts)
	if n == 0 {
		return selPoint{}
	}
	// The last piece starting at or before at.
	low, high := 0, n-1
	for low < high {
		mid := (low + high + 1) / 2
		if text.starts[mid] <= at {
			low = mid
		} else {
			high = mid - 1
		}
	}
	i := low
	if at < text.starts[i] {
		return selPoint{i, 0}
	}
	if at <= text.ends[i] {
		return selPoint{i, at - text.starts[i]}
	}
	if end || i+1 >= n {
		return selPoint{i, text.ends[i] - text.starts[i]}
	}
	return selPoint{i + 1, 0}
}

// selection units, what a press selects by.
const (
	byChar = iota
	byWord
	byParagraph
)

// unitAt is the unit around the rune at index i: [start, end).
func (text selText) unitAt(unit, i int) (int, int) {
	switch unit {
	case byWord:
		return wordAt(text.runes, text.object, i)
	case byParagraph:
		return paragraphAt(text.runes, i)
	}
	return i, i
}

// wordAt is the word holding the rune at index i, as a browser's double
// click finds it:
//   - letters, digits, marks and _ hold together, and so do an apostrophe
//     or a dot between letters (don't, e.g, main.go) and a dot or a comma
//     between digits (1,000.5);
//   - an address (a URL, a path, an e-mail) is one word, without the
//     punctuation that closes the sentence around it;
//   - a run of spaces is one;
//   - any other character is a word of its own, with what combines with it
//     (an emoji's skin tone, a joiner and the emoji it joins);
//   - object runes (a formula, a list mark) go together, and with nothing else.
func wordAt(runes []rune, object []bool, i int) (int, int) {
	if len(runes) == 0 {
		return 0, 0
	}
	i = max(0, min(i, len(runes)-1))
	isObject := func(j int) bool { return j >= 0 && j < len(object) && object[j] }
	if isObject(i) {
		start, end := i, i+1
		for start > 0 && isObject(start-1) {
			start--
		}
		for end < len(runes) && isObject(end) {
			end++
		}
		return start, end
	}
	if start, end, ok := addressAt(runes, object, i); ok {
		return start, end
	}
	r := runes[i]
	switch {
	case unicode.IsSpace(r):
		start, end := i, i+1
		for start > 0 && unicode.IsSpace(runes[start-1]) && !isObject(start-1) {
			start--
		}
		for end < len(runes) && unicode.IsSpace(runes[end]) && !isObject(end) {
			end++
		}
		return start, end
	case wordRune(r) || (joinsWord(runes, i) && !isObject(i-1) && !isObject(i+1)):
		start, end := i, i+1
		for start > 0 && !isObject(start-1) && (wordRune(runes[start-1]) || joinsWord(runes, start-1) && !isObject(start-2)) {
			start--
		}
		for end < len(runes) && !isObject(end) && (wordRune(runes[end]) || joinsWord(runes, end) && !isObject(end+1)) {
			end++
		}
		return start, end
	}
	// A character of its own: an emoji with what combines with it.
	start, end := i, i+1
	for start > 0 && (combines(runes[start]) || runes[start-1] == '\u200d') {
		start--
	}
	for end < len(runes) && (combines(runes[end]) || runes[end-1] == '\u200d') {
		end++
	}
	return start, end
}

// wordRune is a character that words are made of.
func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' ||
		unicode.Is(unicode.Pc, r) // connector punctuation, like the underscore
}

// joinsWord tells whether the rune at i holds the word around it together:
// an apostrophe or a dot between letters, a dot or a comma between digits
// (Unicode's word boundaries, UAX #29: MidLetter, MidNumLet, MidNum).
func joinsWord(runes []rune, i int) bool {
	if i <= 0 || i >= len(runes)-1 {
		return false
	}
	before, after := runes[i-1], runes[i+1]
	letters := wordRune(before) && wordRune(after) && !unicode.IsDigit(before) && !unicode.IsDigit(after)
	digits := unicode.IsDigit(before) && unicode.IsDigit(after)
	switch runes[i] {
	case '\'', '’', '·', '\uFF07':
		return letters
	case '.', '\uFE52', '\uFF0E':
		return wordRune(before) && wordRune(after)
	case ',', '\u066C', '\uFE50', '\uFF0C':
		return digits
	}
	return false
}

// combines tells whether r is drawn with the character before it: a mark,
// a variation selector, a skin tone, a joiner.
func combines(r rune) bool {
	return unicode.IsMark(r) || r == '\u200d' || (r >= '\uFE00' && r <= '\uFE0F') ||
		(r >= 0x1F3FB && r <= 0x1F3FF) || (r >= 0xE0020 && r <= 0xE007F)
}

// addressAt finds the address (a URL, a path, an e-mail) that the rune at i
// is part of: the run of characters between spaces, without the punctuation
// that ends a sentence or the brackets and quotes around it.
func addressAt(runes []rune, object []bool, i int) (int, int, bool) {
	stop := func(j int) bool { return unicode.IsSpace(runes[j]) || (j < len(object) && object[j]) }
	if stop(i) {
		return 0, 0, false
	}
	start, end := i, i+1
	for start > 0 && !stop(start-1) {
		start--
	}
	for end < len(runes) && !stop(end) {
		end++
	}
	// The brackets and quotes around it, the punctuation after it.
	for start < end && strings.ContainsRune("([{<\"'«“‘`", runes[start]) {
		start++
	}
	for end > start {
		last := runes[end-1]
		if strings.ContainsRune(".,;:!?\"'»”’`>", last) {
			end--
			continue
		}
		// A closing bracket is the address's own when it opens one too:
		// https://en.wikipedia.org/wiki/Go_(programming_language).
		if closer := strings.IndexRune(")]}", last); closer >= 0 {
			opener := rune("([{"[closer])
			opens, closes := 0, 0
			for _, r := range runes[start:end] {
				if r == opener {
					opens++
				} else if r == last {
					closes++
				}
			}
			if closes > opens {
				end--
				continue
			}
		}
		break
	}
	if i < start || i >= end || !isAddress(string(runes[start:end])) {
		return 0, 0, false
	}
	return start, end, true
}

// isAddress tells whether a run of text without spaces is a URL, a path or
// an e-mail address.
func isAddress(token string) bool {
	if !strings.ContainsFunc(token, wordRune) {
		return false
	}
	lower := strings.ToLower(token)
	switch {
	case strings.Contains(token, "://"), strings.HasPrefix(lower, "www."), strings.HasPrefix(lower, "mailto:"):
		return true
	case strings.ContainsAny(token, `/\`): // a path: dir/file.go, ~/x, C:\Users
		return true
	}
	// An e-mail: something@domain.tld.
	at := strings.IndexByte(token, '@')
	return at > 0 && strings.Contains(token[at+1:], ".") && !strings.HasSuffix(token, ".")
}

// paragraphAt is the paragraph holding the rune at index i: the text
// between two line breaks or tabs, which end a paragraph, a list item, a
// table cell or the line of a code block.
func paragraphAt(runes []rune, i int) (int, int) {
	if len(runes) == 0 {
		return 0, 0
	}
	i = max(0, min(i, len(runes)-1))
	ends := func(r rune) bool { return r == '\n' || r == '\t' }
	if ends(runes[i]) {
		return i, i
	}
	start, end := i, i+1
	for start > 0 && !ends(runes[start-1]) {
		start--
	}
	for end < len(runes) && !ends(runes[end]) {
		end++
	}
	return start, end
}

// extendSpan is the selection [from, to) a press that selected the unit
// [lo, hi) makes when the pointer is over the rune hit, the caret being at
// caret: it grows by whole units, both ways from the first one (from is
// where it stays, to where the pointer is).
func extendSpan(text selText, unit, lo, hi, hit, caret int) (from, to int) {
	if unit == byChar {
		return lo, caret
	}
	switch {
	case hit < lo:
		start, _ := text.unitAt(unit, hit)
		return hi, min(start, lo)
	case hit >= hi:
		_, end := text.unitAt(unit, hit)
		return lo, max(end, hi)
	}
	return lo, hi
}

// clickCounter counts the presses of a double or triple click: one that
// comes soon after the last, near it, is the next; a fourth starts over.
type clickCounter struct {
	at    fyne.Position
	when  time.Time
	count int
}

// clickSlop is how far the pointer may move between the presses of one
// double or triple click.
const clickSlop = 5

func (counter *clickCounter) press(now time.Time, at fyne.Position, interval time.Duration) int {
	if counter.count > 0 && now.Sub(counter.when) <= interval && distance(at, counter.at) <= clickSlop {
		counter.count = counter.count%3 + 1
	} else {
		counter.count = 1
	}
	counter.at, counter.when = at, now
	return counter.count
}

// reset makes the next press a first one.
func (counter *clickCounter) reset() { counter.count = 0 }
