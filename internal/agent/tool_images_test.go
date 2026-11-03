package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestSplitImageSources(t *testing.T) {
	for _, test := range []struct {
		text, rest string
		sources    []string
	}{
		{"Screenshot 1080x2400", "Screenshot 1080x2400", nil},
		{"Screenshot 1080x2400\n[Image: source: /root/.claude/projects/p/s/tool-results/mcp-1loop_todo-blob-1.jpg]",
			"Screenshot 1080x2400", []string{"/root/.claude/projects/p/s/tool-results/mcp-1loop_todo-blob-1.jpg"}},
		{"[Image: source: /a/tool-results/1.jpg]\n[Image: source: /a/tool-results/2.png]\nState: home screen\n",
			"State: home screen", []string{"/a/tool-results/1.jpg", "/a/tool-results/2.png"}},
		// Only a whole line names a file: quoted in the middle of text, it stays.
		{"echo [Image: source: /x.jpg] done", "echo [Image: source: /x.jpg] done", nil},
	} {
		rest, sources := splitImageSources(test.text)
		if rest != test.rest || !slices.Equal(sources, test.sources) {
			t.Errorf("splitImageSources(%q) = %q %q, want %q %q", test.text, rest, sources, test.rest, test.sources)
		}
	}
}

func TestClaudeResultImages(t *testing.T) {
	data := pngBytes(t)
	encoded := base64.StdEncoding.EncodeToString(data)
	folder := filepath.Join(t.TempDir(), "session", "tool-results")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(folder, "mcp-1loop_todo-blob-1.png")
	if err := os.WriteFile(saved, data, 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, data, 0o644); err != nil {
		t.Fatal(err)
	}
	content := func(blocks ...any) json.RawMessage {
		raw, _ := json.Marshal(blocks)
		return raw
	}
	text := func(s string) any { return map[string]any{"type": "text", "text": s} }
	inline := map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": encoded}}

	for _, test := range []struct {
		name    string
		content json.RawMessage
		text    string
		images  int
	}{
		{"inline picture and its file", content(text("Screenshot 4x3"), inline, text("[Image: source: "+saved+"]")), "Screenshot 4x3", 1},
		{"only the file", content(text("Screenshot 4x3"), text("[Image: source: "+saved+"]")), "Screenshot 4x3", 1},
		{"file deleted", content(text("Screenshot 4x3"), text("[Image: source: "+filepath.Join(folder, "gone.png")+"]")), "Screenshot 4x3", 0},
		{"not a tool result", content(text("[Image: source: " + outside + "]")), "", 0},
		{"plain text", json.RawMessage(`"done"`), "done", 0},
	} {
		got, images := claudeResult(test.content)
		if got != test.text || len(images) != test.images {
			t.Errorf("%s: claudeResult = %q, %d images; want %q, %d", test.name, got, len(images), test.text, test.images)
		}
		for _, part := range images {
			if part.Mime != "image/png" || part.Data != encoded {
				t.Errorf("%s: picture %s of %d bytes, want the PNG", test.name, part.Mime, len(part.Data))
			}
		}
	}
}

func TestToolImagesFromSavedRows(t *testing.T) {
	data := pngBytes(t)
	folder := filepath.Join(t.TempDir(), "tool-results")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(folder, "a.png")
	if err := os.WriteFile(saved, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// A row saved before the pictures were kept: only the paths are left.
	item := ChatItem{Kind: "tool", Tool: "phone_use", Text: "Screenshot\n[Image: source: " + saved + "]\n[Image: source: " + filepath.Join(folder, "gone.jpg") + "]"}
	if got := ToolText(item); got != "Screenshot" {
		t.Errorf("ToolText = %q", got)
	}
	if got := ToolImageCount(item); got != 2 {
		t.Errorf("ToolImageCount = %d, want 2", got)
	}
	parts := ToolImages(item)
	if len(parts) != 2 || parts[0].Data != base64.StdEncoding.EncodeToString(data) || parts[1].Data != "" {
		t.Errorf("ToolImages = %d parts, want the picture then an empty part for the deleted file", len(parts))
	}
	// Kept pictures win, and other rows' text is left alone.
	item.Images = parts[:1]
	if ToolImageCount(item) != 1 || len(ToolImages(item)) != 1 {
		t.Errorf("kept pictures: count %d", ToolImageCount(item))
	}
	user := ChatItem{Kind: "user", Text: "[Image: source: " + saved + "]"}
	if ToolText(user) != user.Text || ToolImageCount(user) != 0 {
		t.Errorf("a user message was changed")
	}
}
