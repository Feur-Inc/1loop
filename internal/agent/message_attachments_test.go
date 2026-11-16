// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"1loop/internal/core"
)

// writePNG saves a w×h PNG at path and returns its bytes.
func writePNG(t *testing.T, path string, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// A message to another chat carries the files it names: pictures as images
// the receiving chat's model sees, other files to copy into its workspace.
func TestChatsToolSendsFiles(t *testing.T) {
	workspace := t.TempDir()
	data := writePNG(t, filepath.Join(workspace, "shot.png"), 4, 3)
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp("", "1loop-test-*.png")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	writePNG(t, tmp.Name(), 2, 2)

	host := &fakeSubchats{list: []SubchatInfo{{ID: "s1", Title: "Fix", State: SubchatDone}}}
	cfg := Config{Chats: host.chats(""), Workspace: workspace}
	output, err, _ := runTool(t, "chats", cfg, true, true, map[string]any{"action": "message", "id": "s1", "text": "Look at this", "files": []string{"shot.png", tmp.Name(), "notes.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.attached) != 1 || len(host.attached[0].Images) != 2 || len(host.attached[0].Files) != 1 {
		t.Fatalf("attached: %+v", host.attached)
	}
	if part := host.attached[0].Images[0]; part.Mime != "image/png" || part.Data != base64.StdEncoding.EncodeToString(data) {
		t.Fatalf("the workspace picture: %s %q", part.Mime, part.Data)
	}
	if file := host.attached[0].Files[0]; file.Name != "notes.txt" || string(file.Data) != "hello" {
		t.Fatalf("the file: %+v", file)
	}
	if host.messages[0] != "s1: Look at this" || !strings.Contains(output, "2 images went with the message") || !strings.Contains(output, "1 file went into its workspace") {
		t.Fatalf("message %q, output %q", host.messages[0], output)
	}

	// The approval of a message to another chat names the files.
	_, _, asked := runTool(t, "chats", cfg, false, false, map[string]any{"action": "message", "id": "other", "text": "See", "files": []string{"shot.png"}})
	if len(asked) != 1 || !strings.Contains(asked[0], "Attached: shot.png") {
		t.Fatalf("asked %q", asked)
	}

	// What cannot be attached is refused with the reason, and nothing is
	// sent.
	if err := os.Mkdir(filepath.Join(workspace, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	big, err := os.Create(filepath.Join(workspace, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	big.Truncate(maxMessageFile + 1)
	big.Close()
	for _, bad := range []struct{ path, want string }{
		{"missing.png", "no such file"},
		{"dir", "it is a folder"},
		{"big.bin", "larger than 20 MiB"},
		{"../../../../etc/passwd", "in the workspace or in /tmp"},
	} {
		sent := len(host.messages)
		_, err, _ := runTool(t, "chats", cfg, true, true, map[string]any{"action": "message", "id": "s1", "text": "x", "files": []string{bad.path}})
		if err == nil || !strings.Contains(err.Error(), bad.want) || !strings.Contains(err.Error(), "Cannot attach "+bad.path) || len(host.messages) != sent {
			t.Fatalf("%s: %v (sent %d)", bad.path, err, len(host.messages)-sent)
		}
	}
	many := make([]string, MaxMessageFiles+1)
	for i := range many {
		many[i] = "shot.png"
	}
	if _, err, _ := runTool(t, "chats", cfg, true, true, map[string]any{"action": "message", "id": "s1", "text": "x", "files": many}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("too many: %v", err)
	}
}

// A subchat's brief carries the files, and the tools say they can.
func TestSubchatStartFiles(t *testing.T) {
	workspace := t.TempDir()
	writePNG(t, filepath.Join(workspace, "mockup.png"), 2000, 1000)
	host := &fakeSubchats{}
	access := host.access("")
	output, err, _ := runTool(t, "subchat", Config{Subchats: access, Chats: fakeChats[access], Workspace: workspace}, true, true,
		map[string]any{"action": "start", "title": "UI", "brief": "Build this screen", "files": []string{"mockup.png"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(host.started) != 1 || len(host.started[0].Attachments.Images) != 1 {
		t.Fatalf("started %+v", host.started)
	}
	// Too wide for a model: scaled down as tool screenshots are.
	raw, _ := base64.StdEncoding.DecodeString(host.started[0].Attachments.Images[0].Data)
	if side := imageSide(raw); side != toolImageSide {
		t.Fatalf("the picture's longest side is %d", side)
	}
	if !strings.Contains(output, "1 image went with the message") {
		t.Fatalf("output %q", output)
	}
	for _, definition := range []map[string]any{subchatToolDefinition(), chatsToolDefinition()} {
		function := definition["function"].(map[string]any)
		parameters := function["parameters"].(map[string]any)
		if _, ok := parameters["properties"].(map[string]any)["files"]; !ok || !strings.Contains(function["description"].(string), "files") {
			t.Fatalf("%s does not offer files", function["name"])
		}
		required := parameters["required"].([]string)
		if required[len(required)-1] != "files" {
			t.Fatalf("%s: files is not required (strict schemas need every field)", function["name"])
		}
	}
}

// The receiving host copies files into its workspace, and the message
// lists them for its agent, apart from the text its transcript shows.
func TestDeliverFiles(t *testing.T) {
	workspace := t.TempDir()
	files, err := DeliverFiles(context.Background(), nil, workspace, []SentFile{{Name: "../spec.md", Data: []byte("# v2")}})
	if err != nil || len(files) != 1 || files[0].Name != "spec.md" {
		t.Fatalf("delivered %+v, %v", files, err)
	}
	if data, err := os.ReadFile(files[0].Path); err != nil || string(data) != "# v2" || filepath.Dir(files[0].Path) != filepath.Join(files[0].Workspace, "tmp") {
		t.Fatalf("copied to %s: %q %v", files[0].Path, data, err)
	}
	message := ChatMessage(ChatSender{ID: "a", Title: "API"}, "", "Read the spec", nil, files)
	if !strings.Contains(message.Content, files[0].Path) || !strings.Contains(message.Content, "The chat that sent this message attached") {
		t.Fatalf("what the agent reads: %q", message.Content)
	}
	if item := MessageItem("i", message); item.Text != "Read the spec" || len(item.Files) != 1 {
		t.Fatalf("the transcript row: %+v", item)
	}
}

// What the result says about the receiving model's sight.
func TestAttachmentsNote(t *testing.T) {
	if AttachmentsNote("claudecode", ChatAttachments{}) != "" {
		t.Fatal("a note without attachments")
	}
	one := ChatAttachments{Images: make([]core.ImagePart, 1)}
	if note := AttachmentsNote("claudecode", one); !strings.Contains(note, "sees them") {
		t.Fatalf("Claude Code: %q", note)
	}
	if note := AttachmentsNote("vllm", one); !strings.Contains(note, "only if it takes images") {
		t.Fatalf("vLLM: %q", note)
	}
	if note := AttachmentsNote("antigravity", one); !strings.Contains(note, "files its Antigravity agent opens") {
		t.Fatalf("Antigravity: %q", note)
	}
}
