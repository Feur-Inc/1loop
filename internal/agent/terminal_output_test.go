// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Reads cut on whole characters, honour a small max_bytes and still move
// forward when it is smaller than a character (#195).
func TestTerminalSpan(t *testing.T) {
	output := []byte("é€😀 line\n")
	total := int64(len(output))
	for offset := int64(0); offset < total; offset++ {
		for limit := int64(1); limit <= total; limit++ {
			from, end := TerminalSpan(output, total, offset, limit)
			if !utf8.Valid(output[from:end]) || end <= from {
				t.Fatalf("offset %d limit %d: %q", offset, limit, output[from:end])
			}
		}
	}
	if from, end := TerminalSpan(output, total, 1, 5); from != 2 || end != 5 {
		t.Fatalf("span %d..%d", from, end)
	}
	if from, end := TerminalSpan(output, total+100, 0, 10); from != 100 || end != 110 {
		t.Fatalf("an offset before the retained output: %d..%d", from, end)
	}
	if limit := TerminalReadLimit(&[]int{5}[0]); limit != 5 {
		t.Fatalf("max_bytes 5 read %d", limit)
	}
}

// terminal_command gives the output without zsh's colored echo, its start
// and end when long, and says when the prompt is back (#206).
func TestTerminalCommandResult(t *testing.T) {
	echo := "e\becho hi\x1b[17D\x1b[32me\x1b[32mc\x1b[32mh\x1b[32mo\x1b[39m\x1b[?2004l\r\r\n"
	prompt := "\x1b[1;32m❯\x1b[0m \x1b[K\x1b[?2004h"
	result := TerminalCommandResult("echo hi", []byte(echo+"hi\r\n"+prompt), 100)
	if !strings.HasPrefix(result["output"].(string), "hi\r\n") || result["prompt_back"] != true || result["next_offset"] != int64(100+len(echo)+4+len(prompt)) {
		t.Fatalf("result: %#v", result)
	}
	if CommandFinished("sleep 9", []byte(echo)) {
		t.Fatal("finished before the prompt")
	}
	if CommandFinished("vim x", []byte(echo+"\x1b[?1049h\x1b[?2004h")) {
		t.Fatal("vim taken for the prompt")
	}
	// A shell with no bracketed paste: the echo is the first line.
	if result := TerminalCommandResult("ls", []byte("ls\r\na\r\nb\r\n$ "), 0); result["output"] != "a\r\nb\r\n$ " || result["prompt_back"] != false {
		t.Fatalf("plain shell: %#v", result)
	}
	// Shell integration gives the status.
	if result := TerminalCommandResult("false", []byte(echo+"\x1b]133;D;1\x07"+prompt), 0); result["command_exit_code"] != 1 {
		t.Fatalf("status: %#v", result)
	}
	long := strings.Repeat("é", 10000) + "TAIL_MARKER"
	result = TerminalCommandResult("x", []byte(echo+long), 0)
	text := result["output"].(string)
	if !utf8.ValidString(text) || !strings.HasSuffix(text, "TAIL_MARKER") || !strings.Contains(text, "bytes cut: terminal_read from offset") || len(text) > 8400 {
		t.Fatalf("long output: %d bytes, %q…", len(text), text[:40])
	}
	if from, to := result["cut_from"].(int64), result["cut_to"].(int64); from < int64(len(echo)) || to <= from {
		t.Fatalf("cut %d..%d", from, to)
	}
}
