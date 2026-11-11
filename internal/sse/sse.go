// SPDX-License-Identifier: MPL-2.0

// Package sse reads server-sent events (text/event-stream), as the HTML
// standard defines them: lines end with LF, CRLF or CR; "data:" lines of
// one event are joined with LF and the event ends at a blank line; one
// space after the colon is dropped; lines starting with ":" are comments.
// An event still open when the stream ends is delivered too, since several
// servers end their stream without the last blank line.
package sse

import (
	"bufio"
	"bytes"
	"io"
	"iter"
	"strings"
)

// Event is one server-sent event.
type Event struct {
	Type string // the "event:" field, "" when absent
	ID   string
	Data string
}

// Events yields the events of r. maxLine bounds one line (0: 1 MiB); a
// longer line, or a read error, ends the sequence with that error.
func Events(r io.Reader, maxLine int) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		if maxLine <= 0 {
			maxLine = 1 << 20
		}
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, min(maxLine, 64<<10)), maxLine)
		scanner.Split(splitLines)
		var parser fields
		for scanner.Scan() {
			if event, ok := parser.line(scanner.Text()); ok && !yield(event, nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			yield(Event{}, err)
			return
		}
		if event, ok := parser.end(); ok {
			yield(event, nil)
		}
	}
}

// Parser splits a stream that arrives in pieces (Write) into events,
// handing each to OnEvent as soon as it is complete.
type Parser struct {
	OnEvent func(Event)
	pending []byte
	parser  fields
}

// Write adds the next piece of the stream.
func (p *Parser) Write(data []byte) (int, error) {
	p.pending = append(p.pending, data...)
	for {
		advance, line, _ := splitLines(p.pending, false)
		if advance == 0 {
			break
		}
		p.pending = p.pending[advance:]
		if event, ok := p.parser.line(string(line)); ok && p.OnEvent != nil {
			p.OnEvent(event)
		}
	}
	if len(p.pending) == 0 {
		p.pending = nil // let a long stream's buffer go
	}
	return len(data), nil
}

// Close ends the stream: a last line and an event without its blank line
// are delivered.
func (p *Parser) Close() error {
	if len(p.pending) > 0 {
		_, line, _ := splitLines(p.pending, true)
		p.pending = nil
		if event, ok := p.parser.line(string(line)); ok && p.OnEvent != nil {
			p.OnEvent(event)
		}
	}
	if event, ok := p.parser.end(); ok && p.OnEvent != nil {
		p.OnEvent(event)
	}
	return nil
}

// splitLines is a bufio.SplitFunc for LF, CRLF and CR line ends. A CR at
// the end of what has arrived waits for the next byte, which may be its LF.
func splitLines(data []byte, atEOF bool) (int, []byte, error) {
	at := bytes.IndexAny(data, "\r\n")
	if at < 0 {
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
	if data[at] == '\n' {
		return at + 1, data[:at], nil
	}
	if at+1 < len(data) {
		if data[at+1] == '\n' {
			return at + 2, data[:at], nil
		}
		return at + 1, data[:at], nil
	}
	if atEOF {
		return at + 1, data[:at], nil
	}
	return 0, nil, nil
}

// fields collects one event's fields line by line.
type fields struct {
	event   Event
	data    strings.Builder
	hasData bool
}

// line reads one line; at a blank line it gives the finished event, if it
// had data.
func (f *fields) line(line string) (Event, bool) {
	if line == "" {
		return f.end()
	}
	if line[0] == ':' {
		return Event{}, false
	}
	name, value, _ := strings.Cut(line, ":")
	value = strings.TrimPrefix(value, " ")
	switch name {
	case "data":
		if f.hasData {
			f.data.WriteByte('\n')
		}
		f.data.WriteString(value)
		f.hasData = true
	case "event":
		f.event.Type = value
	case "id":
		if !strings.ContainsRune(value, 0) {
			f.event.ID = value
		}
	}
	return Event{}, false
}

// end finishes the event being read.
func (f *fields) end() (Event, bool) {
	event, ok := f.event, f.hasData
	event.Data = f.data.String()
	f.event, f.hasData = Event{ID: event.ID}, false // the last ID carries over
	f.data.Reset()
	return event, ok
}
