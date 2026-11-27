// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// TestDiscordArt checks that every state the Discord status names has its
// GIF in assets/discord. UPDATE_DISCORD_ART=1 draws them again from the
// work ring.
func TestDiscordArt(t *testing.T) {
	update := os.Getenv("UPDATE_DISCORD_ART") != ""
	dir := filepath.Join("..", "..", "assets", "discord")
	if update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for state := range discordActivities {
		path := filepath.Join(dir, state+".gif")
		if update {
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := gif.EncodeAll(file, discordGIF(state, 256)); err != nil {
				t.Fatal(err)
			}
			file.Close()
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%v: run go test ./internal/ui -run TestDiscordArt with UPDATE_DISCORD_ART=1", err)
		}
	}
}

func TestPresenceKeepsTheChatPrivateUnlessShared(t *testing.T) {
	started := time.Now().Add(-time.Minute)
	chat := &conversation{Title: "Fix the login bug", Config: agent.Config{Model: "secret-model", Workspace: "/home/me/payroll"}}
	chat.run = &chatRun{started: started, turn: &turn{chat: chat, label: "Running cat .env…", mark: "bash"}}
	ui := &desktopUI{desktopApp: &desktopApp{chats: []*conversation{{Title: "idle chat"}, chat}}}

	activity := ui.presenceActivity()
	shown := activity.Details + " " + activity.State + " " + activity.LargeText
	for _, private := range []string{"login", "payroll", ".env"} {
		if strings.Contains(shown, private) {
			t.Errorf("%q shows without sharing: %q", private, shown)
		}
	}
	if activity.State != "Running a command" || !activity.Start.Equal(started) || !strings.HasSuffix(activity.LargeImage, "/discord/bash.gif") {
		t.Errorf("activity %+v", activity)
	}

	ui.discordDetails = true
	activity = ui.presenceActivity()
	if activity.Details != "payroll · Fix the login bug" || activity.State != "Running cat .env" {
		t.Errorf("shared activity %+v", activity)
	}

	chat.run = nil
	if activity = ui.presenceActivity(); activity.Details != "Idle" || !activity.Start.IsZero() {
		t.Errorf("idle activity %+v", activity)
	}
}

func TestPresenceShowsConnectedRemoteServers(t *testing.T) {
	const srvURL = "https://srv.local:47101"
	remote := &remoteDesktop{
		client:  &remoteClient{address: serverAddress{URL: srvURL}},
		name:    "build-box",
		current: "c1",
		model:   "opus-label",
		models:  []remoteapi.Model{{Label: "opus-label", Name: "Opus 5.5"}},
		chats: []remoteapi.Chat{
			{ID: "c1", Title: "Deploy release", Workspace: "/srv/app", Running: true},
			{ID: "c2", Title: "Other task", Workspace: "/srv/web", Running: true},
		},
		meta: remoteapi.Meta{
			Running: true,
			Model:   "opus-label",
			Mark:    "edit",
			Label:   "Editing main.go…",
		},
	}
	ui := &desktopUI{
		desktopApp: &desktopApp{
			links: []*serverLink{
				// Duplicate of active server: must not double-count.
				{address: serverAddress{URL: srvURL}, answered: true, listing: serverListing{name: "build-box", chats: remote.chats}},
				// Another paired server running a background chat.
				{address: serverAddress{URL: "https://other.local:47101"}, answered: true, listing: serverListing{
					name:  "pi",
					chats: []remoteapi.Chat{{ID: "b1", Title: "Nightly check", Workspace: "/home/pi/repo", Running: true}},
				}},
			},
		},
		server: remote,
	}

	// Private mode: shows model + activity + (+2) for the 3 unique running chats, no titles/paths.
	activity := ui.presenceActivity()
	shown := activity.Details + " " + activity.State + " " + activity.LargeText
	for _, private := range []string{"Deploy", "app", "main.go", "build-box", "Nightly"} {
		if strings.Contains(shown, private) {
			t.Errorf("%q leaked without discordDetails: %q", private, shown)
		}
	}
	if activity.Details != "Working with Opus 5.5" || activity.State != "Editing files (+2)" || !strings.HasSuffix(activity.LargeImage, "/discord/edit.gif") {
		t.Fatalf("remote private activity = %+v", activity)
	}
	firstStart := activity.Start
	if firstStart.IsZero() {
		t.Fatal("expected non-zero Start time for running remote chat")
	}

	// Shared details mode + waiting for approval.
	ui.discordDetails = true
	remote.meta.Approval = &remoteapi.Approval{ID: "a1", Title: "Run?"}
	activity = ui.presenceActivity()
	if activity.Details != "app · Deploy release" || activity.State != "Editing main.go (+2)" || !strings.HasSuffix(activity.LargeImage, "/discord/approval.gif") {
		t.Fatalf("remote shared approval activity = %+v", activity)
	}
	if !activity.Start.Equal(firstStart) {
		t.Fatalf("Start time changed between polls: %v -> %v", firstStart, activity.Start)
	}

	// Leave active server; background paired server chat still shows in presence.
	ui.server = nil
	ui.links[0].listing.chats = nil
	ui.discordDetails = false
	activity = ui.presenceActivity()
	if activity.Details != "Working on a server" || activity.State != "Thinking" {
		t.Fatalf("background server private activity = %+v", activity)
	}
	ui.discordDetails = true
	activity = ui.presenceActivity()
	if activity.Details != "repo · Nightly check" || activity.State != "Thinking" {
		t.Fatalf("background server shared activity = %+v", activity)
	}

	// Once all remote chats stop, presence returns to Idle and clears tracked start times.
	ui.links[1].listing.chats[0].Running = false
	activity = ui.presenceActivity()
	if activity.Details != "Idle" || !activity.Start.IsZero() || len(ui.presenceStarted) != 0 {
		t.Fatalf("idle after remote stop: activity=%+v started=%v", activity, ui.presenceStarted)
	}
}
