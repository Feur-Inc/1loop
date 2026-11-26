// SPDX-License-Identifier: MPL-2.0

package core

import (
	"bufio"
	"bytes"
	"io"
)

// LineReader reads lines as a bufio.Scanner does, each in memory of its own:
// a scanner keeps a buffer as large as its longest line (megabytes, for a
// picture) as long as it lives, and its lines must be copied.
type LineReader struct {
	reader *bufio.Reader
	max    int
	line   []byte
	err    error
}

// NewLineReader reads lines of less than max bytes.
func NewLineReader(from io.Reader, max int) *LineReader {
	return &LineReader{reader: bufio.NewReaderSize(from, 64<<10), max: max}
}

// Scan reads the next line; false at the end, or on an error (Err).
func (lines *LineReader) Scan() bool {
	lines.line = nil
	if lines.err != nil {
		return false
	}
	var line []byte
	for {
		part, err := lines.reader.ReadSlice('\n')
		if len(line)+len(part) >= lines.max {
			lines.err = bufio.ErrTooLong
			return false
		}
		line = append(line, part...)
		switch {
		case err == bufio.ErrBufferFull:
			continue
		case err != nil:
			lines.err = err
			if len(line) == 0 {
				return false
			}
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		lines.line = bytes.TrimSuffix(line, []byte{'\r'})
		return true
	}
}

// Bytes is the line read, the caller's to keep.
func (lines *LineReader) Bytes() []byte { return lines.line }

// Err is what ended the reading, nil at the end of the input.
func (lines *LineReader) Err() error {
	if lines.err == io.EOF {
		return nil
	}
	return lines.err
}
