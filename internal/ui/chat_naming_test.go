// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// On the desktop (#425), the agent's chats tool renames its own chat, and
// the naming model titles a new chat from its first prompt's words only;
// a title chosen meanwhile is kept.
func TestDesktopChatNaming(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var mu sync.Mutex
	var bodies []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(data))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"Parser crash fix"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
	}))
	defer model.Close()
	ui := sidebarUI(t)
	ui.backend = &agent.App{}
	ui.folder, ui.header = newRow("", nil, styleOutline, nil), newFitLabel("", 13, colText)
	chat := ui.chats[0]

	if err := ui.chatAccess(chat).Rename(context.Background(), "  Parser\n work "); err != nil || chat.Title != "Parser work" {
		t.Fatalf("rename: %v %q", err, chat.Title)
	}

	connection := agent.Connection{ID: "c", Name: "Local", Provider: "compatible", BaseURL: model.URL, Enabled: true, AllModels: true}
	ui.choices = map[string]agent.ModelChoice{"namer · Local": {Connection: connection, ID: "namer"}}
	prompt := agent.PromptMessage("The parser crashes on empty files", []core.ImagePart{{Mime: "image/png", Data: "PICTUREBYTES"}}, []agent.FileAttachment{{Name: "dump.bin", Path: "/home/u/app/dump.bin"}})
	title := func() (got string) {
		ui.onUI(context.Background(), func() { got = chat.Title })
		return got
	}

	// No naming model: nothing is asked.
	ui.nameNewChat(chat, prompt)
	time.Sleep(100 * time.Millisecond)
	if len(bodies) != 0 || title() != "Parser work" {
		t.Fatalf("without a naming model: %d requests, %q", len(bodies), title())
	}

	ui.namingModel = &agent.NamingModel{ConnectionID: "c", Model: "namer"}
	ui.nameNewChat(chat, prompt)
	deadline := time.Now().Add(10 * time.Second)
	for title() != "Parser crash fix" {
		if time.Now().After(deadline) {
			t.Fatalf("title %q", title())
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	body := bodies[0]
	mu.Unlock()
	if !strings.Contains(body, "The parser crashes on empty files") || strings.Contains(body, "PICTUREBYTES") || strings.Contains(body, "dump.bin") {
		t.Fatalf("what the naming model got: %s", body)
	}

	// Renamed before the title came: the rename stays.
	ui.onUI(context.Background(), func() { chat.Title = "Before" })
	ui.nameNewChat(chat, prompt)
	ui.onUI(context.Background(), func() { chat.Title = "Mine" })
	time.Sleep(300 * time.Millisecond)
	if got := title(); got != "Mine" {
		t.Fatalf("a rename made meanwhile was overwritten: %q", got)
	}
}
