// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"bytes"
	"image/png"
	"testing"
)

func TestSummarizeTray(t *testing.T) {
	idle := trayChat{title: "idle"}
	working := func(title string, started int64) trayChat {
		return trayChat{title: title, running: true, started: started}
	}
	for _, c := range []struct {
		name   string
		chats  []trayChat
		want   trayStatus
		labels string
	}{
		{"no chats", nil, trayStatus{trayIdle, -1, 0}, "No task running"},
		{"nothing running", []trayChat{idle, idle}, trayStatus{trayIdle, -1, 0}, "No task running"},
		{"one working", []trayChat{idle, working("build", 1)}, trayStatus{trayWorking, 1, 0}, "Working · build"},
		{"the run started last", []trayChat{working("old", 1), working("new", 5), working("mid", 3)}, trayStatus{trayWorking, 1, 2}, "Working · new (+2)"},
		{"waiting beats working", []trayChat{working("a", 9), {title: "ask", running: true, waiting: true, started: 1}}, trayStatus{trayWaiting, 1, 0}, "Waiting for you · ask"},
		{"failed beats working", []trayChat{working("a", 9), {title: "broke", failed: true}}, trayStatus{trayError, 1, 0}, "Failed · broke"},
		{"waiting beats failed", []trayChat{{title: "broke", failed: true}, {title: "ask", running: true, waiting: true}}, trayStatus{trayWaiting, 1, 0}, "Waiting for you · ask"},
		{"two waiting: the first asked", []trayChat{{title: "one", running: true, waiting: true, started: 2}, {title: "two", running: true, waiting: true, started: 1}}, trayStatus{trayWaiting, 0, 1}, "Waiting for you · one (+1)"},
		{"untitled", []trayChat{working("", 1)}, trayStatus{trayWorking, 0, 0}, "Working · New chat"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := summarizeTray(c.chats)
			if got != c.want {
				t.Fatalf("summarizeTray = %+v, want %+v", got, c.want)
			}
			title := ""
			if got.chat >= 0 {
				title = c.chats[got.chat].title
			}
			if label := got.label(title); label != c.labels {
				t.Errorf("label = %q, want %q", label, c.labels)
			}
		})
	}
}

func TestTrayHostDecision(t *testing.T) {
	for _, c := range []struct {
		name                       string
		watcher, registered, known bool
		want                       bool
	}{
		{"no watcher: GNOME without the extension, niri without a tray bar", false, false, false, false},
		{"no watcher, whatever else", false, true, true, false},
		{"watcher with a host: KDE, Waybar", true, true, true, true},
		{"watcher without a host", true, false, true, false},
		{"watcher that does not say", true, false, false, true},
	} {
		if got := trayHostDecision(c.watcher, c.registered, c.known); got != c.want {
			t.Errorf("%s: trayHostDecision(%v, %v, %v) = %v, want %v", c.name, c.watcher, c.registered, c.known, got, c.want)
		}
	}
}

func TestTrayIcons(t *testing.T) {
	for _, state := range []trayState{trayIdle, trayWorking, trayWaiting, trayError} {
		img, err := png.Decode(bytes.NewReader(trayIconFor(state).Content()))
		if err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if size := img.Bounds().Size(); size.X != trayIconSize || size.Y != trayIconSize {
			t.Errorf("%s: %v, want %d px", state, size, trayIconSize)
		}
		if _, _, _, a := img.At(trayIconSize/2, 7).RGBA(); a == 0 && state != trayWorking && state != trayWaiting {
			t.Errorf("%s: no ring at the top", state)
		}
	}
}
