// SPDX-License-Identifier: MPL-2.0

// genstate writes a synthetic 1loop state for the performance runs
// (docs/perf/2026-11-audit.md): a config folder to use as XDG_CONFIG_HOME,
// never the one in use.
//
//	go run ./scripts/perf/genstate -out /tmp/1loop-perf [-items 4000] [-chats 200] [-projects 30] [-theme aurora]
//
// The longest chat has -items rows: user prompts, answers with markdown,
// lists and code blocks, tool calls with output, edits with diffs and a
// picture every 150 rows. The other chats are short, a third of them are
// subchats, spread over -projects folders made under the output.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

type item struct {
	Kind   string   `json:"kind"`
	Text   string   `json:"text"`
	Images []image_ `json:"images,omitempty"`
	Tool   string   `json:"tool,omitempty"`
	Args   string   `json:"args,omitempty"`
	Diff   string   `json:"diff,omitempty"`
	Millis int64    `json:"millis,omitempty"`
}

type image_ struct {
	Mime string `json:"mime"`
	Data string `json:"data"`
}

type chat struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Config  map[string]any `json:"config"`
	History []any          `json:"history"`
	Items   []item         `json:"items"`
	Subchat map[string]any `json:"subchat,omitempty"`
}

var words = strings.Fields(`the transcript layout measures each item at the column width and keeps
only the ones near the viewport visible so that scrolling a long conversation costs about the same
as a short one while streaming an answer re-parses the markdown every sixty milliseconds`)

func sentence(r *rand.Rand, n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = words[r.Intn(len(words))]
	}
	return strings.Join(parts, " ")
}

func answer(r *rand.Rand) string {
	var b strings.Builder
	b.WriteString(sentence(r, 30+r.Intn(60)) + ".\n\n")
	if r.Intn(3) == 0 {
		b.WriteString("- **" + sentence(r, 3) + "**: " + sentence(r, 12) + "\n- `code` " + sentence(r, 10) + "\n- [a link](https://example.com) " + sentence(r, 8) + "\n\n")
	}
	if r.Intn(3) == 0 {
		b.WriteString("```go\nfunc (layout *transcriptLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {\n")
		for i := 0; i < 5+r.Intn(25); i++ {
			fmt.Fprintf(&b, "\ty += measure(objects[%d], size.Width) // %s\n", i, sentence(r, 4))
		}
		b.WriteString("}\n```\n\n")
	}
	b.WriteString(sentence(r, 20+r.Intn(40)) + ".")
	return b.String()
}

func picture(seed int) image_ {
	img := image.NewRGBA(image.Rect(0, 0, 640, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 640; x++ {
			img.Set(x, y, color.RGBA{uint8(x + seed), uint8(y), uint8(x ^ y), 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return image_{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(b.Bytes())}
}

func longChat(r *rand.Rand, n int, workspace string) []item {
	items := make([]item, 0, n)
	for len(items) < n {
		user := item{Kind: "user", Text: sentence(r, 8+r.Intn(30))}
		if len(items)%150 == 0 {
			user.Images = []image_{picture(len(items))}
		}
		items = append(items, user)
		for t := 0; t < 1+r.Intn(6) && len(items) < n; t++ {
			switch r.Intn(4) {
			case 0:
				items = append(items, item{Kind: "tool", Tool: "shell", Args: `{"command":"go test ./internal/ui"}`, Millis: 1200,
					Text: strings.Repeat("ok  \t1loop/internal/ui\t0.42s\n", 1+r.Intn(30))})
			case 1:
				items = append(items, item{Kind: "tool", Tool: "read_file", Args: `{"path":"internal/ui/transcript.go"}`, Millis: 3,
					Text: strings.Repeat("// "+sentence(r, 10)+"\n", 40)})
			case 2:
				items = append(items, item{Kind: "tool", Tool: "edit_file", Args: `{"path":"internal/ui/transcript.go","old_text":"a","new_text":"b"}`, Millis: 5,
					Diff: "--- a/internal/ui/transcript.go\n+++ b/internal/ui/transcript.go\n@@ -1,3 +1,3 @@\n-old line\n+new line\n context\n"})
			default:
				items = append(items, item{Kind: "tool", Tool: "search_files", Args: `{"pattern":"Layout"}`, Millis: 40, Text: strings.Repeat("internal/ui/x.go:12: Layout\n", 10)})
			}
		}
		if len(items) < n {
			items = append(items, item{Kind: "assistant", Text: answer(r)})
		}
	}
	return items
}

func main() {
	out := flag.String("out", "/tmp/1loop-perf", "config folder to write (used as XDG_CONFIG_HOME)")
	longest := flag.Int("items", 4000, "rows of the longest chat")
	chats := flag.Int("chats", 200, "chats and subchats in the sidebar")
	projects := flag.Int("projects", 30, "projects")
	theme := flag.String("theme", "", "a theme of docs/themes to use (aurora: a GLSL shader background)")
	flag.Parse()
	r := rand.New(rand.NewSource(42))

	var folders []string
	for i := 0; i < *projects; i++ {
		folder := filepath.Join(*out, "projects", fmt.Sprintf("project-%02d", i))
		if err := os.MkdirAll(folder, 0o755); err != nil {
			log.Fatal(err)
		}
		folders = append(folders, folder)
	}
	var list []*chat
	for i := 0; i < *chats; i++ {
		workspace := folders[i%len(folders)]
		c := &chat{ID: fmt.Sprintf("perf-%04d", i), Title: strings.Title(sentence(r, 4)), History: []any{},
			Config: map[string]any{"provider": "compatible", "workspace": workspace}}
		if i == 0 {
			c.Title, c.Items = "Long chat", longChat(r, *longest, workspace)
		} else {
			c.Items = longChat(r, 6+r.Intn(30), workspace)
			if i%3 == 0 {
				c.Subchat = map[string]any{"parent": list[r.Intn(len(list))].ID, "ended": "done"}
			}
		}
		list = append(list, c)
	}
	state := map[string]any{"config": map[string]any{"provider": "compatible", "workspace": folders[0]},
		"chats": list, "projects": folders, "connections": []any{}, "approvalMode": "ask",
		"claudeOffered": true, "trayNoticeShown": true, "crashReportsOff": true}
	if *theme != "" {
		themes := filepath.Join(*out, "1loop", "themes")
		os.MkdirAll(themes, 0o755)
		matches, _ := filepath.Glob(filepath.Join("docs", "themes", *theme+".*"))
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				log.Fatal(err)
			}
			os.WriteFile(filepath.Join(themes, filepath.Base(path)), data, 0o644)
		}
		state["theme"] = "file:" + *theme
	}
	data, err := json.Marshal(state)
	if err != nil {
		log.Fatal(err)
	}
	os.MkdirAll(filepath.Join(*out, "1loop"), 0o700)
	if err := os.WriteFile(filepath.Join(*out, "1loop", "state.json"), data, 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %d chats, longest %d rows, %.1f MB state\n", *out, len(list), *longest, float64(len(data))/1e6)
}
