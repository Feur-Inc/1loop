// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
)

func TestNoFolderChat(t *testing.T) {
	server, root, call := testServer(t)
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "c", Model: "m", BaseURL: "http://127.0.0.1:1", Workspace: root}
	body, _ := json.Marshal(map[string]any{"text": "make me a page", "noFolder": true})
	code, data := call("POST", "/api/send", bytes.NewReader(body))
	if code != 200 {
		t.Fatalf("send: %d %s", code, data)
	}
	server.mu.Lock()
	chat := server.chats[0]
	folder := chat.Config.Workspace
	summary := server.summariesLocked()[0]
	projects := slices.Clone(server.projects)
	server.mu.Unlock()
	if !chat.Config.InScratch() || !agent.IsScratchWorkspace(server.scratchBase(), folder) || !summary.Scratch {
		t.Fatalf("the chat has its own folder: %+v", chat.Config)
	}
	if slices.Contains(projects, folder) {
		t.Fatal("its folder is no project")
	}
	if code, data := call("GET", "/api/files?workspace="+url.QueryEscape(folder), nil); code != 200 {
		t.Fatalf("the phone browses its files: %d %s", code, data)
	}
	waitIdle(t, server, chat)

	// Kept as long as the chat, across restarts.
	server.mu.Lock()
	if err := server.saveLocked(); err != nil {
		t.Fatal(err)
	}
	server.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	again, err := newHeadlessServer(ctx, root, server.dir, "")
	if err != nil {
		t.Fatal(err)
	}
	defer again.close()
	if len(again.chats) != 1 || !again.chats[0].Config.InScratch() || slices.Contains(again.projects, folder) {
		t.Fatal("the chat and its folder are kept, and its folder stays no project")
	}

	if code, data := call("POST", "/api/chats/"+chat.ID+"/delete", nil); code != 200 {
		t.Fatalf("delete: %d %s", code, data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, err := os.Stat(folder); err == nil; _, err = os.Stat(folder) {
		if time.Now().After(deadline) {
			t.Fatal("deleting the chat deletes its folder")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal("the projects stay")
	}
}

func waitIdle(t *testing.T, server *headlessServer, chat *serverChat) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		server.mu.Lock()
		running := chat.run != nil
		server.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the run does not end")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServerMoveAddsTheProject(t *testing.T) {
	server, _, _ := testServer(t)
	path, err := agent.NewScratchWorkspace(server.scratchBase())
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path+"/page.html", []byte("<p>"), 0o644)
	chat := &serverChat{ID: "c", Config: agent.Config{Workspace: path, Scratch: path}}
	server.chats = []*serverChat{chat}
	target := t.TempDir()
	server.mu.Lock()
	cfg := server.runConfigLocked(chat, nil)
	if cfg.Move == nil {
		server.mu.Unlock()
		t.Fatal("a scratch chat's run can move it")
	}
	cfg.Move.Set(target)
	note := server.finishMoveLocked(chat, cfg.Move)
	projects := slices.Clone(server.projects)
	server.mu.Unlock()
	if note == "" || chat.Config.Workspace != target || !slices.Contains(projects, target) {
		t.Fatalf("moved, the folder is a project: %q %+v %v", note, chat.Config, projects)
	}
	if _, err := os.Stat(target + "/page.html"); err != nil {
		t.Fatal("the files are copied")
	}
}

func TestRenameChat(t *testing.T) {
	server, root, call := testServer(t)
	server.cfg = agent.Config{Provider: "compatible", ConnectionID: "c", Model: "m", BaseURL: "http://127.0.0.1:1", Workspace: root}
	body, _ := json.Marshal(map[string]any{"text": "hello"})
	if code, data := call("POST", "/api/send", bytes.NewReader(body)); code != 200 {
		t.Fatalf("send: %d %s", code, data)
	}
	server.mu.Lock()
	chat := server.chats[0]
	id := chat.ID
	server.mu.Unlock()
	waitIdle(t, server, chat)

	rename := func(chatID, title string) (int, string) {
		raw, _ := json.Marshal(map[string]any{"title": title})
		code, data := call("POST", "/api/chats/"+chatID+"/rename", bytes.NewReader(raw))
		return code, string(data)
	}
	if code, data := rename(id, "  A new\n title  "); code != 200 {
		t.Fatalf("rename: %d %s", code, data)
	}
	server.mu.Lock()
	got := chat.Title
	server.mu.Unlock()
	if got != "A new title" {
		t.Fatalf("title %q", got)
	}
	long := strings.Repeat("é", 120)
	if code, _ := rename(id, long); code != 200 {
		t.Fatalf("long title: %d", code)
	}
	server.mu.Lock()
	got = chat.Title
	server.mu.Unlock()
	if len([]rune(got)) != 80 {
		t.Fatalf("a long title keeps 80 characters, got %d", len([]rune(got)))
	}
	if code, _ := rename(id, "   "); code != 400 {
		t.Fatalf("a blank title is refused: %d", code)
	}
	if code, _ := rename("nope", "x"); code != 404 {
		t.Fatalf("an unknown chat: %d", code)
	}
}
