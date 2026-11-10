// SPDX-License-Identifier: MPL-2.0

package sse

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const sample = ": a comment\r\n" +
	"event: delta\r\n" +
	"id: 7\r\n" +
	"data: {\"a\":1}\r\n" +
	"\r\n" +
	"data:first\r" +
	"data:  second\r" +
	"\r" +
	"event: no-data\n" +
	"\n" +
	"data: [DONE]" // no blank line, no line end

var want = []Event{
	{Type: "delta", ID: "7", Data: `{"a":1}`},
	{ID: "7", Data: "first\n second"},
	{ID: "7", Data: "[DONE]"},
}

func TestEvents(t *testing.T) {
	var got []Event
	for event, err := range Events(strings.NewReader(sample), 0) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, event)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestEventsStop(t *testing.T) {
	count := 0
	for range Events(strings.NewReader(sample), 0) {
		count++
		break
	}
	if count != 1 {
		t.Errorf("count = %d", count)
	}
}

func TestEventsLongLine(t *testing.T) {
	var last error
	for _, err := range Events(strings.NewReader("data: "+strings.Repeat("x", 100)+"\n\n"), 32) {
		last = err
	}
	if last == nil {
		t.Error("a line over the limit gave no error")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("cut") }

func TestEventsReadError(t *testing.T) {
	for _, err := range Events(failingReader{}, 0) {
		if err == nil || err.Error() != "cut" {
			t.Errorf("err = %v", err)
		}
	}
}

// Every split of the stream, including a CRLF cut in two, gives the same events.
func TestParserPieces(t *testing.T) {
	for cut := 0; cut <= len(sample); cut++ {
		var got []Event
		parser := &Parser{OnEvent: func(event Event) { got = append(got, event) }}
		parser.Write([]byte(sample[:cut]))
		parser.Write([]byte(sample[cut:]))
		parser.Close()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("cut at %d: got %q", cut, got)
		}
	}
	var got []Event
	parser := &Parser{OnEvent: func(event Event) { got = append(got, event) }}
	for i := range sample {
		parser.Write([]byte{sample[i]})
	}
	parser.Close()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("byte by byte: got %q", got)
	}
}
