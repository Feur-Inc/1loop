// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	registry "1loop/internal/settings"
)

// Every setting the desktop shows has its row marked on its page: a search
// result never leads to a page without it.
func TestEverySettingIsMarked(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var source strings.Builder
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source.Write(data)
	}
	constants := map[string]string{"KeyApprovalMode": registry.KeyApprovalMode, "KeyDynamicCompaction": registry.KeyDynamicCompaction,
		"KeyMemory": registry.KeyMemory, "KeyWrapUp": registry.KeyWrapUp, "KeyAutoResume": registry.KeyAutoResume, "KeyWorktreeSort": registry.KeyWorktreeSort,
		"KeyDiscordPresence": registry.KeyDiscordPresence, "KeyDiscordDetails": registry.KeyDiscordDetails,
		"KeyTheme": registry.KeyTheme, "KeyAccent": registry.KeyAccent, "KeyAnimations": registry.KeyAnimations, "KeyCrashReports": registry.KeyCrashReports}
	marked := map[string]bool{}
	for _, match := range regexp.MustCompile(`\.mark\((?:"([^"]+)"|registry\.(\w+))`).FindAllStringSubmatch(source.String(), -1) {
		if match[1] != "" {
			marked[match[1]] = true
		} else {
			marked[constants[match[2]]] = true
		}
	}
	// The shortcuts' page marks its rows in a loop over them.
	if strings.Contains(source.String(), "range registry.Shortcuts() {") && strings.Contains(source.String(), "view.mark(setting.Key, ") {
		for _, setting := range registry.Shortcuts() {
			marked[setting.Key] = true
		}
	}
	for _, setting := range registry.On(registry.OnDesktop) {
		if !marked[setting.Key] {
			t.Errorf("%s (%s) has no marked row on the desktop", setting.Key, setting.Title)
		}
	}
}

// A shortcut's key reads back as it is stored, through Fyne's names.
func TestShortcutKeys(t *testing.T) {
	for _, setting := range registry.Shortcuts() {
		shortcut, ok := fyneShortcut(setting.Default)
		if !ok || chordOf(shortcut) != setting.Default {
			t.Errorf("%s: %s reads back as %q", setting.Key, setting.Default, chordOf(shortcut))
		}
	}
	for value, label := range map[string]string{"Ctrl+K": "Ctrl K", "Alt+ArrowUp": "Alt ↑", "Ctrl+Shift+,": "Ctrl Shift ,", "off": ""} {
		if got := chordLabel(value); got != label {
			t.Errorf("chordLabel(%q) = %q, want %q", value, got, label)
		}
	}
}

// findIn finds the nth place of the words across the pieces a message
// draws, a line wrapping between them.
func TestFindIn(t *testing.T) {
	layer := &selectLayer{}
	pieces := []selPiece{{runes: []rune("Hello brave"), after: " "}, {runes: []rune("new World, hello"), after: "\n"}}
	source := &pieceSource{pieces: pieces}
	layer.under = container.NewStack(source)
	box := container.NewStack(layer)
	for _, c := range []struct {
		query      string
		nth        int
		start, end selPoint
	}{
		{"hello", 0, selPoint{0, 0}, selPoint{0, 5}},
		{"hello", 1, selPoint{1, 11}, selPoint{1, 16}},
		{"brave new", 0, selPoint{0, 6}, selPoint{1, 3}},
		{"world", 0, selPoint{1, 4}, selPoint{1, 9}},
		{"hello", 5, selPoint{1, 11}, selPoint{1, 16}}, // fewer drawn than written: the last
	} {
		got, start, end, ok := findIn(box, c.query, c.nth)
		if !ok || got != layer || start != c.start || end != c.end {
			t.Errorf("findIn(%q, %d) = %v %v %v, want %v %v", c.query, c.nth, start, end, ok, c.start, c.end)
		}
	}
	if _, _, _, ok := findIn(box, "absent", 0); ok {
		t.Error("found what is not there")
	}
}

type pieceSource struct {
	widget.BaseWidget
	pieces []selPiece
}

func (source *pieceSource) selPieces() []selPiece { return source.pieces }
func (source *pieceSource) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack())
}
