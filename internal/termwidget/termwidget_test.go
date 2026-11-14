// SPDX-License-Identifier: MPL-2.0

//go:build !js

package termwidget

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
func (discard) Close() error                { return nil }

// feed shows output in a terminal of about 100×30 cells, as a program in
// the panel would print it, and returns the screen's text. A panic in the
// terminal fails the test with the sequence that caused it, and so does one
// its safety net caught and logged: each handler must cope on its own.
func feed(t *testing.T, output string) (text string) {
	t.Helper()
	test.NewTempApp(t)
	term := New()
	w := test.NewTempWindow(t, term)
	w.Resize(fyne.NewSize(820, 500))
	term.Resize(fyne.NewSize(800, 480))

	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("terminal panicked on %q: %v", output, r)
		}
		if strings.Contains(logged.String(), "dropped output") {
			t.Fatalf("terminal dropped output on %q:\n%s", output, logged.String())
		}
	}()
	_ = term.RunWithConnection(discard{}, strings.NewReader(output))
	return term.Text()
}

// TestEscapesPastTheLineDoNotCrash covers #450: a program asked to delete
// characters with the cursor past the end of the line's text, and the
// slice of the line the terminal took ran past its capacity.
func TestEscapesPastTheLineDoNotCrash(t *testing.T) {
	cases := map[string]string{
		"delete chars past the text (#450)": "short line of thirty-seven characters\x1b[1;56H\x1b[55P",
		"delete more chars than the line":   "short\x1b[1;2H\x1b[55P",
		"insert chars past the text":        "ab\x1b[1;40H\x1b[3@",
		"insert chars on an empty row":      "\x1b[20;1H\x1b[2@",
		"insert a huge count of chars":      "abc\x1b[1;2H\x1b[999999999@",
		"private mode without a number":     "\x1b[h\x1b[l",
		"multibyte final character":         "\x1b[1é",
		"reverse index below the screen":    "\x1b[1;500r\x1b[500;1H\x1bM\x1bD",
		"reverse index on a new terminal":   "\x1bM",
		"restore a cursor saved off screen": "\x1b[24;80H\x1b7\x1b[1;1H\x1b8\x1b[5P\x1b[5@",
		"insert a huge count of lines":      "\x1b[999999999L",
		"scroll up a huge count of lines":   "\x1b[999999999S",
		"short OSC 7 directory":             "\x1b]7;file:/\x07\x1b]7;file://h\x07",
		"device attribute with a prefix":    "\x1b[>c\x1b[=c",
	}
	for name, output := range cases {
		t.Run(name, func(t *testing.T) { feed(t, output) })
	}
}

// TestDeleteCharsKeepsTheLine checks the fix still deletes what it should.
func TestDeleteCharsKeepsTheLine(t *testing.T) {
	if got := feed(t, "abcdef\x1b[1;3H\x1b[2P"); !strings.HasPrefix(got, "abef") {
		t.Errorf("CSI 2 P at column 3 of abcdef: got %q, want abef", got)
	}
	if got := feed(t, "abcdef\x1b[1;3H\x1b[55P"); !strings.HasPrefix(got, "ab") || strings.Contains(got, "c") {
		t.Errorf("CSI 55 P at column 3 of abcdef: got %q, want ab", got)
	}
	if got := feed(t, "abcdef\x1b[1;3H\x1b[2@"); !strings.HasPrefix(got, "ab  cdef") {
		t.Errorf("CSI 2 @ at column 3 of abcdef: got %q, want ab  cdef", got)
	}
}

// TestOSC7LeavesTheProcessDirectory: a shell reporting its directory must
// not move the whole app's working directory.
func TestOSC7LeavesTheProcessDirectory(t *testing.T) {
	before, _ := os.Getwd()
	feed(t, "\x1b]7;file://"+t.TempDir()+"\x07")
	if after, _ := os.Getwd(); after != before {
		_ = os.Chdir(before)
		t.Fatalf("OSC 7 changed the process directory from %s to %s", before, after)
	}
}

// TestEveryCSIWithOddCountsDoesNotCrash runs every CSI final character with
// counts of 0, 1 and past the screen, the cursor at a few places of a short
// line. Printer mode (i) is left out: it would swallow the rest.
func TestEveryCSIWithOddCountsDoesNotCrash(t *testing.T) {
	var b strings.Builder
	for final := byte('@'); final <= '~'; final++ {
		if final == 'i' {
			continue
		}
		for _, count := range []string{"", "0", "1", "81", "500", "99999", "1;500", "500;500"} {
			for _, at := range []string{"1;1", "1;5", "1;60", "30;100", "500;500", "12;40"} {
				fmt.Fprintf(&b, "text\x1b[%sH\x1b[%s%c", at, count, final)
			}
		}
	}
	feed(t, b.String())
}

// TestAPanicDropsTheOutputNotTheApp checks the safety net under every
// handler: output a handler fails on is logged and dropped, and what
// follows still shows.
func TestAPanicDropsTheOutputNotTheApp(t *testing.T) {
	test.NewTempApp(t)
	term := New()
	test.NewTempWindow(t, term).Resize(fyne.NewSize(820, 500))
	term.Resize(fyne.NewSize(800, 480))
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)

	// A title change sends the configuration to listeners; a closed one
	// makes the send panic.
	listener := make(chan Config, 1)
	term.AddListener(listener)
	close(listener)
	_ = term.RunWithConnection(discard{}, strings.NewReader("\x1b]2;title\x07after"))
	if !strings.Contains(logged.String(), "dropped output") {
		t.Errorf("the panic was not logged: %q", logged.String())
	}
	if got := term.Text(); !strings.HasPrefix(got, "after") {
		t.Errorf("output after the panic: got %q, want after", got)
	}
}
