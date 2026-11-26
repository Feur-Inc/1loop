// SPDX-License-Identifier: MPL-2.0

package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// Conversations are long lists of messages, which hold pictures: one can be
// hundreds of megabytes of JSON. Encoded or decoded whole, it takes several
// times that in memory for a moment, which the process then keeps for
// minutes. Here their lists are written and read element by element, and the
// JSON is byte for byte what encoding/json makes of the whole.

// JSONWriter writes JSON piece by piece: what it holds is one element.
type JSONWriter struct {
	Written int64
	to      io.Writer
	buffer  bytes.Buffer
	encoder *json.Encoder
	err     error
}

func NewJSONWriter(to io.Writer) *JSONWriter {
	writer := &JSONWriter{to: to}
	writer.encoder = json.NewEncoder(&writer.buffer)
	return writer
}

// Err is the first error met; nothing is written after it.
func (writer *JSONWriter) Err() error { return writer.err }

// Raw writes JSON made elsewhere.
func (writer *JSONWriter) Raw(data []byte) {
	if writer.err != nil || len(data) == 0 {
		return
	}
	n, err := writer.to.Write(data)
	writer.Written += int64(n)
	writer.err = err
}

// Value writes v as json.Marshal encodes it.
func (writer *JSONWriter) Value(v any) {
	if writer.err != nil {
		return
	}
	writer.buffer.Reset()
	if writer.err = writer.encoder.Encode(v); writer.err != nil {
		return
	}
	writer.Raw(bytes.TrimSuffix(writer.buffer.Bytes(), []byte{'\n'}))
}

// WriteJSONList writes a list, one writing each element.
func WriteJSONList[T any](writer *JSONWriter, list []T, one func(*T)) {
	if list == nil {
		writer.Raw([]byte("null"))
		return
	}
	writer.Raw([]byte{'['})
	for i := range list {
		if i > 0 {
			writer.Raw([]byte{','})
		}
		one(&list[i])
	}
	writer.Raw([]byte{']'})
}

// JSONHole is a list taken out of a value, written in its place by Around.
type JSONHole struct {
	mark []byte
	fill func()
	err  error
}

// NewJSONHole takes the list at field out of its value, which must be the
// caller's own copy: mark, an element found nowhere else in the JSON (it
// carries a JSONMark), holds its place. A list with nothing in it stays.
func NewJSONHole[T any](writer *JSONWriter, field *[]T, mark T, one func(*T)) JSONHole {
	list := *field
	if len(list) == 0 {
		return JSONHole{}
	}
	*field = []T{mark}
	encoded, err := json.Marshal(*field)
	return JSONHole{encoded, func() { WriteJSONList(writer, list, one) }, err}
}

// JSONMark is a text found nowhere else.
func JSONMark() string { return "hole-" + Identifier() }

// Around writes v with its holes filled.
func (writer *JSONWriter) Around(v any, holes ...JSONHole) {
	if writer.err != nil {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		writer.err = err
		return
	}
	type cut struct {
		from, to int
		fill     func()
	}
	var cuts []cut
	for _, hole := range holes {
		if hole.err != nil {
			writer.err = hole.err
			return
		}
		if hole.fill == nil {
			continue
		}
		at := bytes.Index(data, hole.mark)
		if at < 0 {
			writer.err = fmt.Errorf("a list has no place in its JSON")
			return
		}
		cuts = append(cuts, cut{at, at + len(hole.mark), hole.fill})
	}
	slices.SortFunc(cuts, func(a, b cut) int { return a.from - b.from })
	at := 0
	for _, cut := range cuts {
		writer.Raw(data[at:cut.from])
		cut.fill()
		at = cut.to
	}
	writer.Raw(data[at:])
}

// JSONReader reads JSON piece by piece, the other way. It only finds where
// each value ends: json.Unmarshal checks and decodes it.
type JSONReader struct {
	from  *bufio.Reader
	value []byte // the last value read, in a buffer the next one takes
	texts map[string]string
}

func NewJSONReader(from io.Reader) *JSONReader {
	return &JSONReader{from: bufio.NewReaderSize(from, 256<<10), texts: map[string]string{}}
}

// sharedText is the length from which equal texts are kept once.
const sharedText = 256

// Share makes a text the one already read when they are equal: a picture is
// in the conversation as shown, in the model's history and in the chat's
// other versions.
func (reader *JSONReader) Share(text *string) {
	if len(*text) < sharedText {
		return
	}
	if known, ok := reader.texts[*text]; ok {
		*text = known
		return
	}
	reader.texts[*text] = *text
}

// next skips blanks and is the byte after them, left to be read.
func (reader *JSONReader) next() (byte, error) {
	for {
		c, err := reader.from.ReadByte()
		if err != nil {
			return 0, err
		}
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			return c, reader.from.UnreadByte()
		}
	}
}

// skip reads the byte expected next.
func (reader *JSONReader) skip(expected byte) error {
	c, err := reader.next()
	if err == nil && c != expected {
		err = fmt.Errorf("%q instead of %q in the JSON", c, expected)
	}
	if err == nil {
		_, err = reader.from.Discard(1)
	}
	return err
}

// read finds the next value, into reader.value.
func (reader *JSONReader) read() error {
	reader.value = reader.value[:0]
	first, err := reader.next()
	if err != nil {
		return err
	}
	if first != '"' && first != '{' && first != '[' { // a number, true, false or null
		for {
			c, err := reader.from.ReadByte()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			switch c {
			case ',', ']', '}', ':', ' ', '\n', '\r', '\t':
				return reader.from.UnreadByte()
			}
			reader.value = append(reader.value, c)
		}
	}
	depth, inString, escaped := 0, false, false
	for {
		if _, err := reader.from.Peek(1); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		window, _ := reader.from.Peek(reader.from.Buffered())
		i, quote := 0, -1 // quote: the first '"' at i or after, when known
		done := false
		for i < len(window) && !done {
			if !inString {
				switch window[i] {
				case '"':
					inString = true
				case '{', '[':
					depth++
				case '}', ']':
					depth--
					done = depth <= 0
				}
				i++
				continue
			}
			if escaped {
				escaped = false
				i++
				continue
			}
			// Long texts: from one quote or backslash to the next.
			if quote < i {
				if quote = bytes.IndexByte(window[i:], '"'); quote < 0 {
					quote = len(window)
				} else {
					quote += i
				}
			}
			if slash := bytes.IndexByte(window[i:quote], '\\'); slash >= 0 {
				escaped = true
				i += slash + 1
				continue
			}
			i = quote
			if quote < len(window) {
				inString = false
				done = depth == 0
				i++
			}
		}
		reader.value = append(reader.value, window[:i]...)
		reader.from.Discard(i)
		if done {
			return nil
		}
	}
}

// Value reads the next value into v.
func (reader *JSONReader) Value(v any) error {
	if err := reader.read(); err != nil {
		return err
	}
	return json.Unmarshal(reader.value, v)
}

// End checks that nothing follows.
func (reader *JSONReader) End() error {
	c, err := reader.next()
	if err == io.EOF {
		return nil
	}
	if err == nil {
		err = fmt.Errorf("%q after the end of the JSON", c)
	}
	return err
}

// null reads the null a value may be.
func (reader *JSONReader) null() (bool, error) {
	if c, err := reader.next(); err != nil || c != 'n' {
		return false, err
	}
	if err := reader.read(); err != nil {
		return false, err
	}
	if string(reader.value) != "null" {
		return false, fmt.Errorf("%.20q in the JSON", reader.value)
	}
	return true, nil
}

// each reads what is between two delimiters, one reading each element.
func (reader *JSONReader) each(open, end byte, one func() error) error {
	if err := reader.skip(open); err != nil {
		return err
	}
	c, err := reader.next()
	if err != nil {
		return err
	}
	if c == end {
		return reader.skip(end)
	}
	for {
		if err := one(); err != nil {
			return err
		}
		c, err := reader.next()
		if err != nil {
			return err
		}
		if c == end {
			return reader.skip(end)
		}
		if err := reader.skip(','); err != nil {
			return err
		}
	}
}

// Object reads an object: each of its values named in lists is read by that
// function, and rest is an object of the others, for json.Unmarshal. A null
// gives a nil rest.
func (reader *JSONReader) Object(lists map[string]func() error) (rest []byte, err error) {
	if null, err := reader.null(); err != nil || null {
		return nil, err
	}
	rest = []byte{'{'}
	err = reader.each('{', '}', func() error {
		var key string
		if c, err := reader.next(); err != nil || c != '"' {
			return fmt.Errorf("no name in the JSON: %q, %v", c, err)
		}
		if err := reader.Value(&key); err != nil {
			return err
		}
		if lists[key] == nil {
			if len(rest) > 1 {
				rest = append(rest, ',')
			}
			rest = append(append(rest, reader.value...), ':')
		}
		if err := reader.skip(':'); err != nil {
			return err
		}
		if read := lists[key]; read != nil {
			return read()
		}
		if err := reader.read(); err != nil {
			return err
		}
		if len(reader.value) == 0 {
			return fmt.Errorf("no value for %q in the JSON", key)
		}
		rest = append(rest, reader.value...)
		return nil
	})
	return append(rest, '}'), err
}

// ReadJSONList reads a list, one reading each element; nil for a null.
func ReadJSONList[T any](reader *JSONReader, one func(*T) error) ([]T, error) {
	if null, err := reader.null(); err != nil || null {
		return nil, err
	}
	list := []T{}
	err := reader.each('[', ']', func() error {
		var element T
		err := one(&element)
		list = append(list, element)
		return err
	})
	if err != nil {
		return nil, err
	}
	return list, nil
}
