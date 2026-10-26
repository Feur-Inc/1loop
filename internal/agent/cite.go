package agent

// cite shows the user code of the project as a card in the conversation,
// which opens in 1loop's file viewer at those lines: the agent points at
// the code it talks about instead of pasting it.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// citeMaxLines is what a card holds; the viewer opens the whole range.
const citeMaxLines = 60

func citeToolDefinition() map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name":        "cite",
		"description": "Show the user code from a file of the workspace as a card in the conversation, which they can open in 1loop's file viewer at those lines. Use it to point at the code you are talking about rather than pasting it: start_line and end_line (1-based, inclusive) cite a part; null for both cites the whole file. The card shows at most 60 lines, and the reply holds them.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{
			"path":       map[string]any{"type": "string"},
			"start_line": map[string]any{"type": []string{"integer", "null"}},
			"end_line":   map[string]any{"type": []string{"integer", "null"}},
		}, "required": []string{"path", "start_line", "end_line"}, "additionalProperties": false},
	}}
}

// citeHeader starts a citation's output: the card reads its range from it.
var citeHeader = regexp.MustCompile(`^Cited (.+), lines (\d+)-(\d+) of (\d+)\.$`)

// citeFile reads the cited lines: a header line, then the code.
func citeFile(root string, raw string) (string, error) {
	var args struct {
		Path  string `json:"path"`
		Start *int   `json:"start_line"`
		End   *int   `json:"end_line"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", err
	}
	path, err := WorkspacePath(root, args.Path)
	if err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	start, end := 1, 0
	if args.Start != nil {
		start = *args.Start
	}
	if args.End != nil {
		end = *args.End
	}
	if start < 1 || end != 0 && end < start {
		return "", fmt.Errorf("Give a line range from 1, with end_line not before start_line")
	}
	var lines []string
	total := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		total++
		if total == 1 && !utf8.Valid(scanner.Bytes()) {
			return "", fmt.Errorf("%s is not a text file", args.Path)
		}
		if total >= start && (end == 0 || total <= end) && len(lines) < citeMaxLines {
			lines = append(lines, strings.TrimRight(scanner.Text(), "\r"))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if end == 0 || end > total {
		end = total
	}
	if start > total {
		return "", fmt.Errorf("%s has %d lines", args.Path, total)
	}
	shown := args.Path
	if rel, err := filepath.Rel(root, path); err == nil {
		shown = filepath.ToSlash(rel)
	}
	return fmt.Sprintf("Cited %s, lines %d-%d of %d.\n%s", shown, start, end, total, strings.Join(lines, "\n")), nil
}

// Citation is a cite call's card: the file, its cited range and the lines
// the card shows (at most citeMaxLines of the range).
type Citation struct {
	Path              string
	Start, End, Total int
	Code              []string
}

// CitationOf reads a finished cite row.
func CitationOf(item ChatItem) (Citation, bool) {
	if item.Tool != "cite" || item.Failed != "" {
		return Citation{}, false
	}
	header, code, _ := strings.Cut(item.Text, "\n")
	match := citeHeader.FindStringSubmatch(header)
	if match == nil {
		return Citation{}, false
	}
	citation := Citation{Path: match[1], Code: strings.Split(code, "\n")}
	citation.Start, _ = strconv.Atoi(match[2])
	citation.End, _ = strconv.Atoi(match[3])
	citation.Total, _ = strconv.Atoi(match[4])
	return citation, true
}
