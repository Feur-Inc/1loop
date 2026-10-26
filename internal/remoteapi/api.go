// Package remoteapi is what the desktop's phone remote
// (internal/ui/phone_server.go) and the headless server (internal/server)
// share: the JSON written on the wire, and how a phone's input reaches a
// terminal or a browser tab.
package remoteapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/core"
)

func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// PairedDevice is a phone allowed to drive 1loop; only its token's hash is kept.
type PairedDevice struct {
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash"`
	Added     time.Time `json:"added"`
}

type Chat struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	Running   bool   `json:"running"`
	Items     int    `json:"items"`
}

type Item struct {
	ID       string    `json:"id,omitempty"`
	Versions *Versions `json:"versions,omitempty"`
	Kind     string    `json:"kind"`
	Text     string    `json:"text,omitempty"`
	Images   int       `json:"images,omitempty"`
	Tool     string    `json:"tool,omitempty"`
	Verb     string    `json:"verb,omitempty"`
	Target   string    `json:"target,omitempty"`
	Mono     bool      `json:"mono,omitempty"`
	Meta     string    `json:"meta,omitempty"`
	Tone     string    `json:"tone,omitempty"` // of the meta: muted, added, danger, amber
	State    string    `json:"state,omitempty"`
	Args     string    `json:"args,omitempty"`
	Diff     string    `json:"diff,omitempty"`
	Millis   int64     `json:"millis,omitempty"`
	Failed   string    `json:"failed,omitempty"`
}

type Meta struct {
	ContextStatus string    `json:"contextStatus,omitempty"`
	Chat          string    `json:"chat"`
	Title         string    `json:"title"`
	Running       bool      `json:"running"`
	Label         string    `json:"label,omitempty"`
	Mark          string    `json:"mark,omitempty"`
	Queue         []string  `json:"queue"`
	Steer         []string  `json:"steer"`
	Items         int       `json:"items"`
	Approval      *Approval `json:"approval,omitempty"`
	Question      *Question `json:"question,omitempty"` // request_user_input waiting for answers
	// The tool executing now, whatever the label says (an approval replaces it):
	// the phone's status badge lets touches through while phone_use runs.
	Tool string `json:"tool,omitempty"`
	// The chat's model, by its label in the model list, and context use.
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Tier   string `json:"tier,omitempty"` // its speed (see agent.TierFor)
	Used   int    `json:"used"`
	Window int    `json:"window"`
	Todo   []Todo `json:"todo"` // the chat's task list
	// Its background commands; POST /api/chats/{id}/tasks/{task}/stop ends one.
	Tasks []agent.BackgroundTaskInfo `json:"tasks"`
	// Its account's type: a local phone keeps a ChatGPT (Web) chat's page open.
	Provider string `json:"provider,omitempty"`
}

// Page is the part of a chat's items GET /api/chats/{id} returns: all of
// them, or with ?limit=N the N before ?before= (the end by default), so a
// long chat opens on its last messages and the rest loads on demand.
func Page(req *http.Request, total int) (first, end int) {
	end = total
	if before, err := strconv.Atoi(req.URL.Query().Get("before")); err == nil && before >= 0 && before < total {
		end = before
	}
	limit, err := strconv.Atoi(req.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		return 0, end
	}
	return max(0, end-limit), end
}

// Question is the model's request_user_input; the answers go back with
// POST /api/answer {id, answers: {question id: label or "Other: …"}}.
type Question struct {
	Chat      string           `json:"chat"`
	ID        string           `json:"id"`
	Questions []agent.Question `json:"questions"`
}

type Approval struct {
	Chat    string `json:"chat"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
}

type Effort struct {
	Value string `json:"value"`
	Label string `json:"label"`
	About string `json:"about,omitempty"` // "Balanced", as under the desktop's slider
}

type Model struct {
	Label   string   `json:"label"` // unique key, as in the desktop picker
	Name    string   `json:"name"`
	Detail  string   `json:"detail"`
	Account string   `json:"account,omitempty"` // its connection's ID
	Family  string   `json:"family,omitempty"`  // the picker groups by it, sorted newest first
	Efforts []Effort `json:"efforts"`
	// The speeds the plan offers (none on most), and the one used unless told.
	ServiceTiers       []agent.ServiceTier `json:"serviceTiers,omitempty"`
	DefaultServiceTier string              `json:"defaultServiceTier,omitempty"`
}

// Account is a connection of the model picker; problem says why its
// models could not be listed.
type Account struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Kind     string `json:"kind"` // the provider's name, "ChatGPT account"
	Problem  string `json:"problem,omitempty"`
}

// Limits is how much of an account's usage windows is left.
type Limits struct {
	Account string        `json:"account"`
	Note    string        `json:"note,omitempty"`
	Windows []LimitWindow `json:"windows"`
}

type LimitWindow struct {
	Label string  `json:"label"`
	Left  float64 `json:"left"`            // percent
	Reset int64   `json:"reset,omitempty"` // Unix milliseconds
}

type Tab struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Index int    `json:"index,omitempty"` // a tool tab: its item in the conversation
}

type Todo struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Started   int64  `json:"started,omitempty"` // Unix milliseconds
	Completed int64  `json:"completed,omitempty"`
}

var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78DEHMc]`)

// PlainTerminal turns a terminal's raw output into text: no escape
// sequences, and a line rewritten after a carriage return shows its end.
func PlainTerminal(raw []byte) string {
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

func BrowserInputFromPhone(ctx context.Context, b *browser.Page, action string, x, y, dy float64, text string) error {
	info := b.Snapshot()
	px, py := x*float64(info.Width), y*float64(info.Height)
	switch action {
	case "tap":
		b.Input(input.DispatchMouseEvent(input.MouseMoved, px, py))
		b.Input(input.DispatchMouseEvent(input.MousePressed, px, py).WithButton(input.Left).WithClickCount(1))
		b.Input(input.DispatchMouseEvent(input.MouseReleased, px, py).WithButton(input.Left).WithClickCount(1))
	case "scroll":
		b.Input(input.DispatchMouseEvent(input.MouseWheel, px, py).WithDeltaY(dy * float64(info.Height)))
	case "text", "line":
		if text != "" {
			b.Input(input.InsertText(text))
		}
		if action == "line" {
			b.Input(chromedp.KeyEvent(kb.Enter))
		}
	case "key":
		key := map[string]string{"Enter": kb.Enter, "Backspace": kb.Backspace, "Tab": kb.Tab, "Escape": kb.Escape}[text]
		if key == "" {
			return fmt.Errorf("Unknown key")
		}
		b.Input(chromedp.KeyEvent(key))
	case "navigate", "back", "forward", "reload":
		return b.NavigateSoon(action, text)
	default:
		return fmt.Errorf("Unknown browser action")
	}
	return nil
}

// Versions identifies either prompt wordings or retries of their response.
type Versions struct {
	Anchor  string `json:"anchor"`
	Current int    `json:"current"` // one-based
	Total   int    `json:"total"`
}

func ItemVersions(state *agent.VersionState, index int) *Versions {
	item := state.Items[index]
	anchor := item.ID
	if item.Kind == "assistant" || item.Kind == "error" {
		for i := index - 1; i >= 0; i-- {
			if state.Items[i].Kind == "user" {
				anchor = state.Items[i].ID + ":reply"
				break
			}
		}
	} else if item.Kind != "user" {
		return nil
	}
	family, selected := state.Family(anchor)
	if len(family) < 2 {
		return nil
	}
	return &Versions{Anchor: anchor, Current: selected + 1, Total: len(family)}
}

// GetHooks is GET /api/hooks: the hooks file of the machine the agent runs
// on, as text, with how many hooks it holds.
func GetHooks(w http.ResponseWriter, _ *http.Request) {
	content, err := agent.ReadHooks()
	if err != nil {
		core.Fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"content": content, "path": agent.HooksPath(), "count": agent.HookCount()})
}

// SaveHooks is POST /api/hooks {content}: checked, then written. A paired
// device already drives the agent; hooks are commands like its others.
func SaveHooks(w http.ResponseWriter, r *http.Request) {
	var args struct{ Content string }
	if core.ReadJSON(r, &args) != nil {
		core.Fail(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if err := agent.SaveHooks(args.Content); err != nil {
		core.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "count": agent.HookCount()})
}

// ItemImage is GET /api/chats/{id}/items/{index}/images/{n}: picture n of an
// item (a screenshot a tool returned, or one attached to a message), so a
// client shows what the model saw. images gives the item's pictures.
func ItemImage(w http.ResponseWriter, r *http.Request, images func(index int) ([]core.ImagePart, bool)) {
	index, err1 := strconv.Atoi(r.PathValue("index"))
	n, err2 := strconv.Atoi(r.PathValue("n"))
	parts, ok := images(index)
	if err1 != nil || err2 != nil || !ok || n < 0 || n >= len(parts) {
		core.Fail(w, http.StatusNotFound, "No such image")
		return
	}
	data, err := base64.StdEncoding.DecodeString(parts[n].Data)
	if err != nil || !strings.HasPrefix(parts[n].Mime, "image/") {
		core.Fail(w, http.StatusNotFound, "Unreadable image")
		return
	}
	w.Header().Set("Content-Type", parts[n].Mime)
	w.Header().Set("Cache-Control", "private, max-age=86400") // an item's pictures never change
	w.Write(data)
}
