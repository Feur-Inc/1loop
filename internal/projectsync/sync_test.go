package projectsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"1loop/internal/agent"
)

// memoryEngine is a Local engine with its chats in memory, in one folder.
type memoryEngine struct {
	Local
	root  string
	chats map[string]Chat
}

func newEngine(t *testing.T, device string) *memoryEngine {
	engine := &memoryEngine{root: filepath.Join(t.TempDir(), "proj"), chats: map[string]Chat{}}
	engine.Local = Local{
		Device: device,
		Resolve: func(project string, create bool) (string, error) {
			if create {
				os.MkdirAll(engine.root, 0755)
			}
			if _, err := os.Stat(engine.root); err != nil {
				return "", err
			}
			return engine.root, nil
		},
		Chats: func(string) []Chat {
			var out []Chat
			for _, chat := range engine.chats {
				out = append(out, chat)
			}
			return out
		},
		AddChat: func(_ string, chat Chat, conflict bool) (string, error) {
			if conflict {
				chat.ID += "-copy"
				chat.Title += " (from the copy)"
			}
			engine.chats[chat.ID] = chat
			return chat.Title, nil
		},
	}
	return engine
}

func write(t *testing.T, root, name, text string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	os.MkdirAll(filepath.Dir(path), 0755)
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func read(root, name string) string {
	data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	return string(data)
}

func TestCopyBackAndForth(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // the trash
	ctx := context.Background()
	desktop, phone := newEngine(t, "Desktop"), newEngine(t, "Phone")
	os.MkdirAll(desktop.root, 0755)
	write(t, desktop.root, "main.go", "package main\n")
	write(t, desktop.root, "docs/notes.md", "notes\n")
	write(t, desktop.root, "node_modules/big/index.js", "heavy")
	desktop.chats["c1"] = Chat{ID: "c1", Title: "Fix main", Items: []agent.ChatItem{{Kind: "user", Text: "fix"}}}
	bases := &BaseStore{Path: filepath.Join(t.TempDir(), "bases.json")}

	// First copy: everything but the heavy folder.
	report, err := Copy(ctx, desktop, "proj", phone, "proj", bases, false)
	if err != nil {
		t.Fatal(err)
	}
	if read(phone.root, "main.go") != "package main\n" || read(phone.root, "docs/notes.md") != "notes\n" || phone.chats["c1"].Title != "Fix main" {
		t.Fatalf("first copy incomplete: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(phone.root, "node_modules")); err == nil {
		t.Fatal("heavy folders stay where they are")
	}

	// Nothing changed: nothing to send.
	if report, _ := Copy(ctx, desktop, "proj", phone, "proj", bases, true); !report.Plan.Empty() {
		t.Fatalf("an up to date copy sends nothing: %+v", report.Plan)
	}

	// Work continues on the phone; the copy back sends only that.
	write(t, phone.root, "main.go", "package main // phone\n")
	write(t, phone.root, "phone.txt", "new\n")
	chat := phone.chats["c1"]
	chat.Items = append(chat.Items, agent.ChatItem{Kind: "assistant", Text: "done on the phone"})
	phone.chats["c1"] = chat
	report, err = Copy(ctx, phone, "proj", desktop, "proj", bases, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(report.Plan.Copy, ",") != "main.go,phone.txt" || len(report.Plan.Chats) != 1 {
		t.Fatalf("only what changed goes back: %+v", report.Plan)
	}
	if read(desktop.root, "main.go") != "package main // phone\n" || len(desktop.chats["c1"].Items) != 2 {
		t.Fatal("the desktop has the phone's work")
	}

	// Both change the same file and the same chat: both versions are kept.
	write(t, desktop.root, "main.go", "desktop version\n")
	write(t, phone.root, "main.go", "phone version\n")
	dc := desktop.chats["c1"]
	dc.Title = "Renamed on the desktop"
	desktop.chats["c1"] = dc
	pc := phone.chats["c1"]
	pc.Title = "Renamed on the phone"
	phone.chats["c1"] = pc
	report, err = Copy(ctx, desktop, "proj", phone, "proj", bases, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Plan.Conflicts) != 1 || len(report.Plan.ChatConflicts) != 1 {
		t.Fatalf("changes on both sides are conflicts: %+v", report.Plan)
	}
	if read(phone.root, "main.go") != "desktop version\n" {
		t.Fatal("the copy's version takes the name")
	}
	asides, _ := filepath.Glob(filepath.Join(phone.root, "main (before copy from Desktop *).go"))
	if len(asides) != 1 || read(phone.root, filepath.Base(asides[0])) != "phone version\n" {
		t.Fatalf("the phone's own version is kept beside it: %v", asides)
	}
	if phone.chats["c1"].Title != "Renamed on the phone" || phone.chats["c1-copy"].Title != "Renamed on the desktop (from the copy)" {
		t.Fatal("the phone keeps its chat and receives the desktop's as a copy")
	}

	// Deleted on the desktop, untouched on the phone: to the phone's trash.
	os.Remove(filepath.Join(desktop.root, "docs/notes.md"))
	report, err = Copy(ctx, desktop, "proj", phone, "proj", bases, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Plan.Delete) != 1 || report.Result.Deleted != 1 {
		t.Fatalf("a deleted file is removed on the other side: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(phone.root, "docs/notes.md")); err == nil {
		t.Fatal("still on the phone")
	}
	trashed, _ := filepath.Glob(filepath.Join(TrashDir(), "*", "proj", "docs", "notes.md"))
	if len(trashed) != 1 {
		t.Fatal("the deleted file waits in the trash")
	}
}

func TestImportRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	if _, err := inside(root, "../outside"); err == nil {
		t.Fatal("a path out of the project is refused")
	}
	os.Symlink(t.TempDir(), filepath.Join(root, "link"))
	if _, err := inside(root, "link/file"); err == nil {
		t.Fatal("a link on the way is refused")
	}
}

// The same copy between two engines over HTTPS, the certificate pinned.
func TestCopyOverHTTPS(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	desktop, phone := newEngine(t, "Desktop"), newEngine(t, "Phone")
	os.MkdirAll(desktop.root, 0755)
	write(t, desktop.root, "a.txt", "a\n")
	manifest, export, importing := desktop.Local.Routes()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sync/manifest", manifest)
	mux.HandleFunc("POST /api/sync/export", export)
	mux.HandleFunc("POST /api/sync/import", importing)
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	sum := sha256.Sum256(server.Certificate().Raw)
	peer := &Peer{URL: server.URL, Token: "t", Fingerprint: hex.EncodeToString(sum[:])}
	bases := &BaseStore{Path: filepath.Join(t.TempDir(), "bases.json")}
	if _, err := Copy(context.Background(), peer, "proj", phone, "proj", bases, false); err != nil {
		t.Fatal(err)
	}
	if read(phone.root, "a.txt") != "a\n" {
		t.Fatal("pulled from the peer")
	}
	write(t, phone.root, "b.txt", "b\n")
	if _, err := Copy(context.Background(), phone, "proj", peer, "proj", bases, false); err != nil {
		t.Fatal(err)
	}
	if read(desktop.root, "b.txt") != "b\n" {
		t.Fatal("pushed to the peer")
	}
	wrong := &Peer{URL: server.URL, Token: "t", Fingerprint: strings.Repeat("0", 64)}
	if _, err := Copy(context.Background(), wrong, "proj", phone, "proj", bases, true); err == nil {
		t.Fatal("another certificate is refused")
	}
}
