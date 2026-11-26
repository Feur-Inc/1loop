// SPDX-License-Identifier: MPL-2.0

package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"unsafe"
)

// A list in a hole is written as json.Marshal writes it in its place.
func TestJSONHoles(t *testing.T) {
	type element struct {
		Text string `json:"text"`
	}
	type whole struct {
		Name  string    `json:"name"`
		First []element `json:"first"`
		Other []element `json:"other,omitempty"`
		Last  int       `json:"last"`
	}
	for _, value := range []whole{
		{Name: "both <&>", First: []element{{"a "}, {"<b>"}}, Other: []element{{"c"}}, Last: 3},
		{Name: "nil and empty", Other: []element{}},
		{Name: "empty", First: []element{}},
	} {
		want, _ := json.Marshal(value)
		var out bytes.Buffer
		writer := NewJSONWriter(&out)
		copied, mark := value, JSONMark()
		one := func(e *element) { writer.Value(e) }
		writer.Around(&copied, NewJSONHole(writer, &copied.Other, element{mark + "2"}, one), NewJSONHole(writer, &copied.First, element{mark}, one))
		if writer.Err() != nil || out.String() != string(want) || writer.Written != int64(len(want)) {
			t.Errorf("%v, %d bytes\ngot  %s\nwant %s", writer.Err(), writer.Written, out.String(), want)
		}
	}
	writer := NewJSONWriter(io.Discard)
	writer.Value(func() {})
	writer.Raw([]byte("more"))
	if writer.Err() == nil || writer.Written != 0 {
		t.Error("an error did not stop the writing")
	}
}

// Equal long texts read are one in memory.
func TestJSONReaderSharesTexts(t *testing.T) {
	long := strings.Repeat("x", sharedText)
	data, _ := json.Marshal(map[string]any{"list": []string{long, "short", long}, "name": "n"})
	reader := NewJSONReader(bytes.NewReader(data))
	var list []string
	rest, err := reader.Object(map[string]func() error{"list": func() (err error) {
		list, err = ReadJSONList(reader, func(text *string) error { err := reader.Value(text); reader.Share(text); return err })
		return err
	}})
	if err != nil || string(rest) != `{"name":"n"}` || len(list) != 3 || reader.End() != nil {
		t.Fatalf("%v, %s, %d", err, rest, len(list))
	}
	if unsafe.StringData(list[0]) != unsafe.StringData(list[2]) {
		t.Error("the same text is twice in memory")
	}
}

func TestJSONReaderErrors(t *testing.T) {
	for _, data := range []string{``, `{`, `{"a":`, `{"list":{}}`, `{"list":[1,}`, `[]`, `{"a":1`} {
		reader := NewJSONReader(strings.NewReader(data))
		_, err := reader.Object(map[string]func() error{"list": func() error {
			_, err := ReadJSONList(reader, func(n *int) error { return reader.Value(n) })
			return err
		}})
		if err == nil {
			t.Errorf("%q was read", data)
		}
	}
	reader := NewJSONReader(strings.NewReader(`{} {}`))
	if _, err := reader.Object(nil); err != nil || reader.End() == nil {
		t.Error("what follows the end was not noticed")
	}
}

// LineReader reads the lines a Scanner does.
func TestLineReader(t *testing.T) {
	long := strings.Repeat("y", 200_000)
	for _, text := range []string{"", "\n", "a", "a\n", "a\r\nb\r", "\n\na\n", long, long + "\nb\n" + long + "\r\n", "a\n" + strings.Repeat("z", 64<<10) + "\nb"} {
		scanner := bufio.NewScanner(strings.NewReader(text))
		scanner.Buffer(nil, 1<<20)
		lines := NewLineReader(iotest.HalfReader(strings.NewReader(text)), 1<<20)
		for scanner.Scan() {
			if !lines.Scan() || string(lines.Bytes()) != scanner.Text() {
				t.Fatalf("%.20q: %.20q, want %.20q", text, lines.Bytes(), scanner.Text())
			}
		}
		if lines.Scan() || lines.Err() != nil {
			t.Errorf("%.20q: more lines, or %v", text, lines.Err())
		}
	}
	lines := NewLineReader(strings.NewReader("ok\n"+long), 100_000)
	if !lines.Scan() || lines.Scan() || !errors.Is(lines.Err(), bufio.ErrTooLong) {
		t.Errorf("a line too long: %v", lines.Err())
	}
	lines = NewLineReader(io.MultiReader(strings.NewReader("half"), iotest.ErrReader(errors.New("broken"))), 1<<20)
	if !lines.Scan() || string(lines.Bytes()) != "half" || lines.Scan() || lines.Err() == nil {
		t.Errorf("a broken input: %q, %v", lines.Bytes(), lines.Err())
	}
	// Each line is the caller's to keep.
	lines = NewLineReader(strings.NewReader("one\ntwo\n"), 1<<20)
	lines.Scan()
	one := lines.Bytes()
	lines.Scan()
	if string(one) != "one" {
		t.Errorf("the first line became %q", one)
	}
}

// The reader finds where values end whatever is in their texts, and wherever
// its buffer happens to be cut.
func TestJSONReaderFindsTheEndOfValues(t *testing.T) {
	random := rand.New(rand.NewPCG(1, 2))
	pieces := []string{`"`, `\`, `\\`, `\"`, "{", "}", "[", "]", ",", ":", "\n", " ", "é", "x", "null", strings.Repeat("long ", 20)}
	text := func(n int) string {
		var out strings.Builder
		for range n {
			out.WriteString(pieces[random.IntN(len(pieces))])
		}
		return out.String()
	}
	for round := range 60 {
		type element struct {
			Text string         `json:"text"`
			Any  any            `json:"any"`
			Map  map[string]any `json:"map"`
		}
		var want []element
		for range random.IntN(6) {
			want = append(want, element{text(random.IntN(40)), []any{text(3), 1.5, true, nil, map[string]any{}}, map[string]any{text(2): text(2)}})
		}
		if round%20 == 0 { // longer than the buffer, its escapes around the cut
			want = append(want, element{Text: strings.Repeat(`a\"`, 200_000)})
		}
		data, _ := json.MarshalIndent(map[string]any{"list": want, text(2): text(5)}, "", strings.Repeat(" ", round%3))
		var from io.Reader = bytes.NewReader(data)
		switch round % 3 {
		case 1:
			from = iotest.HalfReader(from)
		case 2:
			from = iotest.DataErrReader(from)
		}
		reader := NewJSONReader(from)
		var got []element
		rest, err := reader.Object(map[string]func() error{"list": func() (err error) {
			got, err = ReadJSONList(reader, func(e *element) error { return reader.Value(e) })
			return err
		}})
		if err != nil || reader.End() != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		var whole map[string]any
		json.Unmarshal(data, &whole)
		delete(whole, "list")
		var others map[string]any
		if err := json.Unmarshal(rest, &others); err != nil || !reflect.DeepEqual(others, whole) {
			t.Fatalf("round %d: the rest %.200s, %v", round, rest, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d: %d elements, want %d", round, len(got), len(want))
		}
	}
}
