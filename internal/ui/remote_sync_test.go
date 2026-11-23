// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
