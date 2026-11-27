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
	"1loop/internal/loopy"
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

func TestPresenceShowsLoopy(t *testing.T) {
	since := time.Now().Add(-time.Minute)
	ui := &desktopUI{desktopApp: &desktopApp{loopyModel: "Opus 5.5", loopySince: since}}
	ui.loopyDoing = loopy.Change{State: "reading", Label: "Opening bank.example/login…", Tool: "browser_navigate", Running: true}

	activity := ui.presenceActivity()
	if activity.Details != "Loopy · Opus 5.5" || activity.State != "Browsing the web" || !activity.Start.Equal(since) || !strings.HasSuffix(activity.LargeImage, "/discord/call.gif") {
		t.Errorf("activity %+v", activity)
	}
	ui.discordDetails = true
	if activity = ui.presenceActivity(); activity.State != "Opening bank.example/login" {
		t.Errorf("shared activity %+v", activity)
	}
	ui.loopyDoing = loopy.Change{State: "waiting", Label: "Waiting for you", Running: true}
	if activity = ui.presenceActivity(); activity.State != "Waiting for the user" || !strings.HasSuffix(activity.LargeImage, "/discord/approval.gif") {
		t.Errorf("waiting activity %+v", activity)
	}

	// A chat that started later comes first; Loopy counts among the others.
	chat := &conversation{Title: "chat", Config: agent.Config{Model: "m"}}
	chat.run = &chatRun{started: time.Now(), turn: &turn{chat: chat, mark: "edit"}}
	ui.chats = []*conversation{chat}
	ui.discordDetails = false
	if activity = ui.presenceActivity(); activity.State != "Editing files (+1)" {
		t.Errorf("both %+v", activity)
	}
	chat.run.started = since.Add(-time.Minute)
	if activity = ui.presenceActivity(); activity.State != "Waiting for the user (+1)" {
		t.Errorf("Loopy last %+v", activity)
	}

	chat.run = nil
	ui.loopyDoing = loopy.Change{State: "done", Label: "Done"}
	if activity = ui.presenceActivity(); activity.Details != "Idle" {
		t.Errorf("idle activity %+v", activity)
	}
}
