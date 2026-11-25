// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// limitsServer is a server window whose server answers GET /api/limits
// with status and body, counting the reads.
func limitsServer(t *testing.T, status int, body string) (*desktopUI, *remoteDesktop, *atomic.Int32) {
	t.Helper()
	reads := &atomic.Int32{}
	ui, remote, _ := serverWindow(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/limits" {
			w.Write([]byte("{}"))
			return
		}
		reads.Add(1)
		w.WriteHeader(status)
		w.Write([]byte(body))
	})
	remote.accounts = []remoteapi.Account{
		{ID: "plan", Name: "ChatGPT", Provider: "openai", Kind: "ChatGPT account"},
		{ID: "key", Name: "API key", Provider: "compatible", Kind: "OpenAI-compatible"},
	}
	remote.models = []remoteapi.Model{{Account: "plan", Label: "gpt", Name: "GPT", ID: "gpt"}}
	remote.model = "gpt"
	return ui, remote, reads
}

// menuShows waits for the open model menu to show text.
func menuShows(t *testing.T, ui *desktopUI, text string) {
	t.Helper()
	until(t, fmt.Sprintf("the model menu to show %q", text), func() bool {
		return ui.model.open != nil && slices.Contains(visibleTexts(ui.model.open.content), text)
	})
}

func closeModelMenu(t *testing.T, ui *desktopUI) {
	t.Helper()
	ui.model.open.Hide()
	until(t, "the menu to close", func() bool { return ui.model.open == nil })
}

// The menu shows the server's limits as this computer's: each window's
// share left and reset, and an account with none says so. They show at
// once when the menu opens again, and are read again once a turn ended.
func TestServerLimitsInModelMenu(t *testing.T) {
	reset := time.Date(2026, 11, 25, 18, 30, 0, 0, time.Local)
	body := fmt.Sprintf(`{"accounts":[
		{"account":"plan","windows":[{"label":"5h","left":64,"reset":%d},{"label":"7d","left":12}],"checked":%d},
		{"account":"key","note":"Limits not provided","windows":[]}]}`, reset.UnixMilli(), time.Now().UnixMilli())
	ui, remote, reads := limitsServer(t, 200, body)

	remote.modelMenu()
	menuShows(t, ui, "64 % left")
	menuShows(t, ui, "12 % left")
	if reads.Load() != 1 {
		t.Fatalf("one read on open, got %d", reads.Load())
	}
	ui.model.open.onKey(&fyne.KeyEvent{Name: fyne.KeyDown}) // the second account
	ui.model.open.onKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	menuShows(t, ui, agent.NoLimitsNote) // an older server's "Limits not provided" too
	closeModelMenu(t, ui)

	remote.modelMenu()
	if !slices.Contains(visibleTexts(ui.model.open.content), "64 % left") {
		t.Fatal("the limits read a moment ago show at once")
	}
	closeModelMenu(t, ui)
	if reads.Load() != 1 {
		t.Fatalf("limits under a minute old are not read again, got %d reads", reads.Load())
	}

	remote.chats = []remoteapi.Chat{{ID: "c1", Title: "First", Running: true}}
	remote.show("srv", "", []remoteapi.Chat{{ID: "c1", Title: "First"}}, nil)
	remote.modelMenu()
	menuShows(t, ui, "64 % left") // the old figures meanwhile
	until(t, "a new read after the turn", func() bool { return reads.Load() == 2 })
}

// A server older than /api/limits says so instead of showing nothing.
func TestServerLimitsTooOld(t *testing.T) {
	ui, remote, _ := limitsServer(t, 404, "404 page not found")
	remote.modelMenu()
	menuShows(t, ui, remoteLimitsTooOld)
}

// A failed read keeps the last good numbers, with why they are old.
func TestServerLimitsAfterFailure(t *testing.T) {
	var limits remoteLimits
	if got := limits.of("plan").Note; got != "Loading limits…" {
		t.Fatalf("before any answer: %q", got)
	}
	now := time.Now()
	limits.apply([]remoteapi.Limits{{Account: "plan", Windows: []remoteapi.LimitWindow{{Label: "5h", Left: 40}}}}, nil, now)
	if got := limits.of("plan"); len(got.Windows) != 1 || got.Windows[0].Remaining() != 40 || !got.Checked.Equal(now) {
		t.Fatalf("the answer, read now: %+v", got)
	}
	if got := limits.of("gone"); got.Note != agent.NoLimitsNote {
		t.Fatalf("an account the server does not list: %+v", got)
	}
	if limits.due(now.Add(30*time.Second)) || !limits.due(now.Add(remoteLimitsEvery)) {
		t.Fatal("read again after a minute")
	}
	limits.apply(nil, errors.New("connection refused"), now)
	got := limits.of("plan")
	if len(got.Windows) != 1 || got.Note != "Couldn't read the server's limits: connection refused" {
		t.Fatalf("after a failure: %+v", got)
	}
	limits.apply(nil, remoteError{http.StatusMethodNotAllowed, "Method Not Allowed"}, now)
	if got := limits.of("plan"); got.Note != remoteLimitsTooOld || len(got.Windows) != 0 {
		t.Fatalf("no route, and no numbers left: %+v", got)
	}
}

// In a server window the Usage page says whose usage it shows, the
// server's or this computer's, and a server without /api/usage is said
// to be too old.
func TestServerUsagePageSaysWhere(t *testing.T) {
	ui, remote, _ := serverWindow(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	remote.name = "srv"
	page := ui.usageSettings()
	until(t, "the server's usage", func() bool {
		return slices.Contains(labelTexts(page), "This server is too old to show its usage: update it")
	})
	if texts := visibleTexts(page); !slices.Contains(texts, "srv") || !slices.Contains(texts, "This computer") ||
		!slices.Contains(texts, "The chats run on the server srv, and its accounts' subscription limits.") {
		t.Fatalf("the switch and where the figures live: %q", texts)
	}
	tapText(t, page, "This computer")
	if _, local := ui.usageSource().(localUsage); !local {
		t.Fatal("this computer's ledger")
	}
	texts := visibleTexts(page)
	if !slices.Contains(texts, "Spend") || !slices.Contains(texts, "The chats run on this computer, and its accounts' subscription limits. The server's are under srv.") {
		t.Fatalf("this computer's usage, said so: %q", texts)
	}
}

// labelTexts are the labels' texts in object, which visibleTexts leaves out.
func labelTexts(object fyne.CanvasObject) (texts []string) {
	switch object := object.(type) {
	case *widget.Label:
		texts = append(texts, object.Text)
	case *fyne.Container:
		for _, child := range object.Objects {
			texts = append(texts, labelTexts(child)...)
		}
	}
	return texts
}
