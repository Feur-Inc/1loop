package main

// What the desktop's phone remote (remote.go) and the headless server
// (server_*.go) share: the JSON written on the wire, small HTTP helpers, and
// how a phone's input reaches a terminal or a browser tab.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func writeJSON(writer http.ResponseWriter, status int, v any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(v)
}

func fail(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func readJSON(req *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(v)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// pairedDevice is a phone allowed to drive 1loop; only its token's hash is kept.
type pairedDevice struct {
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash"`
	Added     time.Time `json:"added"`
}

type remoteChat struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	Running   bool   `json:"running"`
	Items     int    `json:"items"`
}

type remoteItem struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Images int    `json:"images,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Verb   string `json:"verb,omitempty"`
	Target string `json:"target,omitempty"`
	Mono   bool   `json:"mono,omitempty"`
	Meta   string `json:"meta,omitempty"`
	Tone   string `json:"tone,omitempty"` // of the meta: muted, added, danger, amber
	State  string `json:"state,omitempty"`
	Args   string `json:"args,omitempty"`
	Diff   string `json:"diff,omitempty"`
	Millis int64  `json:"millis,omitempty"`
	Failed string `json:"failed,omitempty"`
}

type remoteMeta struct {
	ContextStatus string          `json:"contextStatus,omitempty"`
	Chat          string          `json:"chat"`
	Title         string          `json:"title"`
	Running       bool            `json:"running"`
	Label         string          `json:"label,omitempty"`
	Mark          string          `json:"mark,omitempty"`
	Queue         []string        `json:"queue"`
	Steer         []string        `json:"steer"`
	Items         int             `json:"items"`
	Approval      *remoteApproval `json:"approval,omitempty"`
	// The chat's model, by its label in the model list, and context use.
	Model  string       `json:"model"`
	Effort string       `json:"effort"`
	Used   int          `json:"used"`
	Window int          `json:"window"`
	Todo   []remoteTodo `json:"todo"` // the chat's task list
}

type remoteApproval struct {
	Chat    string `json:"chat"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
}

type remoteEffort struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type remoteModel struct {
	Label   string         `json:"label"` // unique key, as in the desktop picker
	Name    string         `json:"name"`
	Detail  string         `json:"detail"`
	Efforts []remoteEffort `json:"efforts"`
}

type remoteTab struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Index int    `json:"index,omitempty"` // a tool tab: its item in the conversation
}

type remoteTodo struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Started   int64  `json:"started,omitempty"` // Unix milliseconds
	Completed int64  `json:"completed,omitempty"`
}

var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78DEHMc]`)

// plainTerminal turns a terminal's raw output into text: no escape
// sequences, and a line rewritten after a carriage return shows its end.
func plainTerminal(raw []byte) string {
	text := ansiSequence.ReplaceAllString(string(raw), "")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if cut := strings.LastIndex(strings.TrimRight(line, "\r"), "\r"); cut >= 0 {
			line = line[cut+1:]
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' || r >= ' ' {
				return r
			}
			return -1
		}, line)
	}
	return strings.Join(lines, "\n")
}

func browserInputFromPhone(ctx context.Context, b *browserPage, action string, x, y, dy float64, text string) error {
	info := b.snapshot()
	px, py := x*float64(info.Width), y*float64(info.Height)
	switch action {
	case "tap":
		b.input(input.DispatchMouseEvent(input.MouseMoved, px, py))
		b.input(input.DispatchMouseEvent(input.MousePressed, px, py).WithButton(input.Left).WithClickCount(1))
		b.input(input.DispatchMouseEvent(input.MouseReleased, px, py).WithButton(input.Left).WithClickCount(1))
	case "scroll":
		b.input(input.DispatchMouseEvent(input.MouseWheel, px, py).WithDeltaY(dy * float64(info.Height)))
	case "text", "line":
		if text != "" {
			b.input(input.InsertText(text))
		}
		if action == "line" {
			b.input(chromedp.KeyEvent(kb.Enter))
		}
	case "key":
		key := map[string]string{"Enter": kb.Enter, "Backspace": kb.Backspace, "Tab": kb.Tab, "Escape": kb.Escape}[text]
		if key == "" {
			return fmt.Errorf("Unknown key")
		}
		b.input(chromedp.KeyEvent(key))
	case "navigate", "back", "forward", "reload":
		return b.navigateSoon(action, text)
	default:
		return fmt.Errorf("Unknown browser action")
	}
	return nil
}
