// SPDX-License-Identifier: MPL-2.0

package agent

// What the terminal tools return of a shell's output, for the desktop and
// the headless server alike.

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TerminalSpan is the part of a terminal's retained output (output, the
// last bytes of total) that a read from offset returns, at most limit bytes:
// whole UTF-8 characters only, so that a page never ends or starts in the
// middle of one, and at least one character when there is one.
func TerminalSpan(output []byte, total, offset, limit int64) (int64, int64) {
	first := total - int64(len(output))
	offset = max(first, min(offset, total))
	for offset < total && !utf8.RuneStart(output[offset-first]) {
		offset++
	}
	end := min(total, offset+max(1, limit))
	for end < total && end > offset && !utf8.RuneStart(output[end-first]) {
		end--
	}
	if end == offset && offset < total {
		// A limit smaller than the character: give the character.
		_, size := utf8.DecodeRune(output[offset-first:])
		end = min(total, offset+int64(size))
	}
	return offset, end
}

// CommandWaitLimit is the longest terminal_command waits for its command.
const CommandWaitLimit = 30000

const (
	commandHead = 2048
	commandTail = 6144
)

var (
	// A shell with bracketed paste (zsh, bash 5.1+) turns it off when it
	// runs a command line and on again at its next prompt.
	pasteOff = []byte("\x1b[?2004l")
	pasteOn  = []byte("\x1b[?2004h")
	// Shell integration (OSC 133) marks a command's end with its status.
	commandEnd = regexp.MustCompile(`\x1b\]133;D;(-?\d+)`)
	ansiCode   = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)
)

// commandStart is where a command's own output begins in what the shell
// wrote after it was sent: past the echo of the command line, which zsh
// colors character by character.
func commandStart(command string, data []byte) int {
	if at := bytes.Index(data, pasteOff); at >= 0 {
		at += len(pasteOff)
		for at < len(data) && data[at] == '\r' {
			at++
		}
		if at < len(data) && data[at] == '\n' {
			at++
		}
		return at
	}
	line, _, found := bytes.Cut(data, []byte("\n"))
	command = strings.TrimSpace(command)
	if found && command != "" && !strings.Contains(command, "\n") && strings.Contains(string(ansiCode.ReplaceAll(line, nil)), command) {
		return len(line) + 1
	}
	return 0
}

// CommandFinished says whether the shell is back at its prompt after the
// command (data is what it wrote since): it turned bracketed paste on
// again, outside a full-screen program, or reported the command's end.
func CommandFinished(command string, data []byte) bool {
	data = data[commandStart(command, data):]
	if commandEnd.Match(data) {
		return true
	}
	on := bytes.LastIndex(data, pasteOn)
	if on < 0 || bytes.LastIndex(data, pasteOff) > on {
		return false
	}
	// vim and other full-screen programs turn bracketed paste on too.
	return bytes.LastIndex(data, []byte("\x1b[?1049h")) <= bytes.LastIndex(data, []byte("\x1b[?1049l"))
}

// TerminalCommandResult is what terminal_command returns of what the shell
// wrote since the command (data, from offset): the output without the
// command's echo, its start and end when it is long, with where to read the
// part left out, whether the prompt is back and, when the shell reports it,
// the command's exit status.
func TerminalCommandResult(command string, data []byte, offset int64) map[string]any {
	start := commandStart(command, data)
	output := data[start:]
	result := map[string]any{
		"next_offset": offset + int64(len(data)),
		"prompt_back": CommandFinished(command, data),
	}
	if match := commandEnd.FindAllSubmatch(output, -1); len(match) > 0 {
		if code, err := strconv.Atoi(string(match[len(match)-1][1])); err == nil {
			result["command_exit_code"] = code
		}
	}
	if len(output) <= commandHead+commandTail {
		result["output"] = string(output)
		return result
	}
	head, _ := TerminalSpan(output, int64(len(output)), commandHead, 0)
	tail, _ := TerminalSpan(output, int64(len(output)), int64(len(output))-commandTail, 0)
	from := offset + int64(start) + head
	result["output"] = string(output[:head]) + fmt.Sprintf("\n[… %d bytes cut: terminal_read from offset %d gives them …]\n", tail-head, from) + string(output[tail:])
	result["cut_from"] = from
	result["cut_to"] = offset + int64(start) + tail
	return result
}
