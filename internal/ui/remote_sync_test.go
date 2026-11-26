// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"1loop/internal/remoteapi"
)

// serverWindow is a window of the test driver working on a server that
// answers with handle, counting the requests it gets. Nothing runs in the
// background: what the window would read is in remote.live.wanted.
func serverWindow(t *testing.T, handle http.HandlerFunc) (*desktopUI, *remoteDesktop, *atomic.Int32) {
	t.Helper()
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	ui, _ := twoWindows(t)
	ui.windows = ui.windows[:1]
	ui.window = test.NewWindow(nil)
	ui.window.SetContent(ui.build())
	ui.window.Resize(fyne.NewSize(1180, 760))
	requests := &atomic.Int32{}
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if handle != nil {
			handle(w, r)
			return
		}
		w.Write([]byte("{}"))
	}))
	t.Cleanup(web.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := &remoteClient{http: web.Client(), address: serverAddress{URL: web.URL}, traffic: &remoteTraffic{}}
	remote := &remoteDesktop{parent: ui, client: client, ctx: ctx, cancel: cancel, window: ui.window, status: ui.serverStatus,
		wake: make(chan struct{}, 1), convs: map[string]*conversation{}, openCommands: map[string]bool{}}
	remote.resetTabs()
	ui.server = remote
	remote.chats = []remoteapi.Chat{{ID: "c1", Title: "First"}, {ID: "c2", Title: "Second"}}
	remote.live.answered = true
	return ui, remote, requests
}

// openRead opens a server chat as if its full read had come with items.
func openRead(remote *remoteDesktop, id string, seq uint64, items ...remoteapi.Item) {
	remote.openChat(id)
	remote.chatCache(id).reading = true
	remote.applyRead(id, remoteChatRead{changed: true, tag: `"t1"`, seq: seq, items: items, meta: remoteapi.Meta{Chat: id}})
	remote.live.wanted.Store(0)
}

func itemEvent(t *testing.T, chat string, index int, item remoteapi.Item, seq uint64) serverEvent {
	t.Helper()
	data, err := json.Marshal(map[string]any{"chat": chat, "index": index, "item": item})
	if err != nil {
		t.Fatal(err)
	}
	return serverEvent{"item", seq, string(data)}
}

// A streamed piece of an answer, a new row and the chat's meta change the
// window as they come, without reading the server again (#591).
func TestServerEventsAppliedWithoutRead(t *testing.T) {
	ui, remote, requests := serverWindow(t, nil)
	openRead(remote, "c1", 10,
		remoteapi.Item{ID: "u1", Kind: "user", Text: "hello"},
		remoteapi.Item{ID: "a1", Kind: "assistant", Text: "Hi"})
	conv := remote.convs["c1"]
	if ui.current != conv || len(conv.Items) != 2 {
		t.Fatalf("opened chat: %d items, on screen %v", len(conv.Items), ui.current == conv)
	}
	before := requests.Load()

	remote.apply(itemEvent(t, "c1", 1, remoteapi.Item{ID: "a1", Kind: "assistant", Text: "Hi there"}, 11))
	remote.apply(itemEvent(t, "c1", 2, remoteapi.Item{ID: "t1", Kind: "tool", Tool: "shell", State: "running"}, 12))
	meta, _ := json.Marshal(remoteapi.Meta{Chat: "c1", Running: true, Label: "Working"})
	remote.apply(serverEvent{"meta", 13, string(meta)})
	chats, _ := json.Marshal(map[string]any{"chats": []remoteapi.Chat{{ID: "c1", Title: "Renamed", Running: true}, {ID: "c2", Title: "Second"}}})
	remote.apply(serverEvent{"chats", 14, string(chats)})

	if conv.Items[1].Text != "Hi there" || len(conv.Items) != 3 || conv.states[2] != "running" {
		t.Fatalf("items after the events: %+v, states %v", conv.Items, conv.states)
	}
	if !remote.meta.Running || remote.meta.Label != "Working" {
		t.Fatalf("meta after its event: %+v", remote.meta)
	}
	if remote.chats[0].Title != "Renamed" || conv.Title != "Renamed" {
		t.Fatalf("sidebar after the chats event: %+v, title %q", remote.chats, conv.Title)
	}
	if wanted := remote.live.wanted.Load(); wanted != 0 || requests.Load() != before {
		t.Fatalf("the events asked for a read: wanted %b, %d requests", wanted, requests.Load()-before)
	}

	// Another chat's events do not touch the one on screen.
	remote.apply(itemEvent(t, "c2", 0, remoteapi.Item{ID: "x", Kind: "user", Text: "elsewhere"}, 15))
	if len(conv.Items) != 3 || remote.live.wanted.Load() != 0 {
		t.Fatalf("another chat's item: %d items, wanted %b", len(conv.Items), remote.live.wanted.Load())
	}
}

// What cannot be applied is read in full: an item past the end of the chat
// (an event missed), a new version, a replace; and the open chat's events
// while it is being read are applied again over the read.
func TestServerEventsFallBackToRead(t *testing.T) {
	_, remote, _ := serverWindow(t, nil)
	openRead(remote, "c1", 10, remoteapi.Item{ID: "u1", Kind: "user", Text: "hello"})
	conv := remote.convs["c1"]

	remote.apply(itemEvent(t, "c1", 5, remoteapi.Item{ID: "late", Kind: "assistant", Text: "?"}, 11))
	if remote.live.wanted.Load()&wantChat == 0 || len(conv.Items) != 1 {
		t.Fatalf("an item past the end: wanted %b, %d items", remote.live.wanted.Load(), len(conv.Items))
	}
	remote.live.wanted.Store(0)
	remote.apply(serverEvent{"replace", 12, `{"chat":"c1"}`})
	if remote.live.wanted.Load()&wantChat == 0 {
		t.Fatal("replace did not read the chat again")
	}
	remote.live.wanted.Store(0)
	remote.apply(serverEvent{"item", 13, `not json`})
	if remote.live.wanted.Load()&wantChat == 0 {
		t.Fatal("an event it cannot read did not read the chat again")
	}

	// A read on its way: the events meanwhile come back over it when newer.
	remote.live.wanted.Store(0)
	cache := remote.chatCache("c1")
	cache.reading = true
	remote.apply(itemEvent(t, "c1", 1, remoteapi.Item{ID: "a1", Kind: "assistant", Text: "old"}, 20))
	remote.apply(itemEvent(t, "c1", 1, remoteapi.Item{ID: "a1", Kind: "assistant", Text: "newer"}, 22))
	remote.applyRead("c1", remoteChatRead{changed: true, seq: 21, items: []remoteapi.Item{
		{ID: "u1", Kind: "user", Text: "hello"}, {ID: "a1", Kind: "assistant", Text: "snapshot"}}, meta: remoteapi.Meta{Chat: "c1"}})
	if len(conv.Items) != 2 || conv.Items[1].Text != "newer" {
		t.Fatalf("after the read: %+v", conv.Items)
	}

	// A chat not read yet takes no item: it is read in full.
	remote.openChat("c2")
	remote.live.wanted.Store(0)
	remote.apply(itemEvent(t, "c2", 0, remoteapi.Item{ID: "x", Kind: "user", Text: "hi"}, 30))
	if remote.live.wanted.Load()&wantChat == 0 {
		t.Fatal("an item of a chat not read yet did not read it")
	}
}

// A read of the state older than the last chats event keeps the event's
// list: the sidebar does not go back.
func TestServerStateOlderThanEventsKeepsThem(t *testing.T) {
	_, remote, _ := serverWindow(t, nil)
	chats, _ := json.Marshal(map[string]any{"chats": []remoteapi.Chat{{ID: "c1", Title: "New title"}}})
	remote.apply(serverEvent{"chats", 50, string(chats)})
	projects := []string{}
	remote.applyState(remoteState{Name: "box", Chats: []remoteapi.Chat{{ID: "c1", Title: "Old title"}}, Projects: &projects},
		remoteChatRead{changed: true, seq: 40}, nil)
	if remote.chats[0].Title != "New title" {
		t.Fatalf("an older state replaced a newer event: %+v", remote.chats)
	}
	remote.applyState(remoteState{Name: "box", Chats: []remoteapi.Chat{{ID: "c1", Title: "Newest"}}, Projects: &projects},
		remoteChatRead{changed: true, seq: 60}, nil)
	if remote.chats[0].Title != "Newest" {
		t.Fatalf("a newer state was not applied: %+v", remote.chats)
	}
}

// A server chat opened before its items came shows a light placeholder,
// not the chat shown before nor an empty page, until they come (#591).
func TestServerChatPlaceholderUntilItsItems(t *testing.T) {
	ui, remote, _ := serverWindow(t, nil)
	openRead(remote, "c1", 1, remoteapi.Item{ID: "u1", Kind: "user", Text: "the first chat"})
	remote.openChat("c2")
	if !ui.chatWaitView.Visible() || len(ui.transcript.Objects) != 0 {
		t.Fatalf("opening a chat not read yet: placeholder %v, %d rows shown", ui.chatWaitView.Visible(), len(ui.transcript.Objects))
	}
	remote.chatCache("c2").reading = true
	remote.applyRead("c2", remoteChatRead{changed: true, seq: 2, items: []remoteapi.Item{{ID: "u2", Kind: "user", Text: "the second"}}, meta: remoteapi.Meta{Chat: "c2"}})
	if ui.chatWaitView.Visible() || len(remote.convs["c2"].Items) != 1 {
		t.Fatalf("after its items: placeholder %v, %d items", ui.chatWaitView.Visible(), len(remote.convs["c2"].Items))
	}
}

// The title bar's activity line shows only for a request slower than
// slowRequest, and goes once the server answered.
func TestServerSlowRequestShowsActivity(t *testing.T) {
	ui, remote, _ := serverWindow(t, nil)
	remote.busy(1)
	remote.busy(-1) // answered at once: never shown
	time.Sleep(slowRequest + 50*time.Millisecond)
	test.Drain()
	if ui.serverBusy.Visible() {
		t.Fatal("a quick request showed the activity line")
	}
	remote.busy(1)
	time.Sleep(slowRequest / 2)
	test.Drain()
	if ui.serverBusy.Visible() {
		t.Fatal("the activity line shows before the request is slow")
	}
	untilDrained(t, "the activity line", ui.serverBusy.Visible)
	remote.busy(-1)
	if ui.serverBusy.Visible() {
		t.Fatal("the activity line stays once the server answered")
	}
}

// Until the server's first answer its chats are not listed (the sidebar
// says it is connecting), rather than an empty list.
func TestServerChatsLoadingUntilFirstAnswer(t *testing.T) {
	ui, remote, _ := serverWindow(t, nil)
	remote.live.answered = false
	if group := ui.serverGroup(remote.client.address.URL); group.known {
		t.Fatal("a server that has not answered lists its chats as known")
	}
	projects := []string{}
	remote.applyState(remoteState{Name: "box", Chats: []remoteapi.Chat{{ID: "c1", Title: "First"}}, Projects: &projects}, remoteChatRead{changed: true}, nil)
	if group := ui.serverGroup(remote.client.address.URL); !group.known || len(group.chats) != 1 {
		t.Fatalf("after its answer: known %v, %d chats", group.known, len(group.chats))
	}
}

// A chat opened again in the session shows at once as it was left, with
// its state, and is then read again only if it changed (If-None-Match).
func TestServerChatCachedWhenOpenedAgain(t *testing.T) {
	var mu sync.Mutex
	var tags []string
	handle := func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/state":
			w.Write([]byte(`{"name":"box","projects":[],"chats":[{"id":"c1","title":"First"},{"id":"c2","title":"Second"}],"settings":{"models":[{"label":"m","name":"Model"}]}}`))
		case "/api/chats/c1", "/api/chats/c2":
			mu.Lock()
			tags = append(tags, r.URL.Path+" "+r.Header.Get("If-None-Match"))
			mu.Unlock()
			w.Header().Set("X-1loop-Seq", "7")
			w.Header().Set("ETag", `"`+r.URL.Path+`"`)
			if r.Header.Get("If-None-Match") == `"`+r.URL.Path+`"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Write([]byte(`{"items":[{"id":"u","kind":"user","text":"hello"},{"id":"a","kind":"assistant","text":"answer"}],"meta":{"chat":"` + r.URL.Path[len("/api/chats/"):] + `","running":true,"label":"Working"}}`))
		default:
			w.Write([]byte(`{"tabs":[]}`))
		}
	}
	ui, remote, _ := serverWindow(t, handle)
	go remote.refreshLoop()
	remote.notify()
	remote.openChat("c1")
	untilDrained(t, "the first read", func() bool { c := remote.live.cache["c1"]; return c != nil && c.loaded && c.hasTabs })
	remote.openChat("c2")
	untilDrained(t, "the other chat", func() bool { c := remote.live.cache["c2"]; return c != nil && c.loaded })

	remote.openChat("c1")
	conv := remote.convs["c1"]
	if ui.current != conv || len(conv.Items) != 2 || len(ui.transcript.Objects) == 0 || ui.chatWaitView.Visible() {
		t.Fatalf("opened again: %d items, %d rows, placeholder %v", len(conv.Items), len(ui.transcript.Objects), ui.chatWaitView.Visible())
	}
	if !remote.meta.Running || remote.meta.Label != "Working" {
		t.Fatalf("its state is not shown at once: %+v", remote.meta)
	}
	untilDrained(t, "the conditional read", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(tags) == 3
	})
	untilDrained(t, "the read to be applied", func() bool { return !remote.live.cache["c1"].reading })
	mu.Lock()
	defer mu.Unlock()
	if tags[2] != `/api/chats/c1 "/api/chats/c1"` {
		t.Fatalf("reads: %q", tags)
	}
	if len(conv.Items) != 2 || len(remote.models) != 1 {
		t.Fatalf("after a 304: %d items, %d models", len(conv.Items), len(remote.models))
	}
}

// The activity line's segment stays within the line as it glides: nothing
// clips it, and it would run over the sidebar.
func TestActivityLineStaysWithin(t *testing.T) {
	test.NewTempApp(t)
	line := newActivityLine()
	line.Resize(fyne.NewSize(400, 2))
	renderer := test.TempWidgetRenderer(t, line)
	for _, phase := range []float32{0, 0.1, 0.5, 0.9, 1} {
		line.phase = phase
		renderer.Layout(line.Size())
		if x, w := line.bar.Position().X, line.bar.Size().Width; x < 0 || x+w > 400 {
			t.Fatalf("phase %v: the segment runs from %v to %v, outside 0 to 400", phase, x, x+w)
		}
	}
}
