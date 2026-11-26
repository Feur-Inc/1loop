// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"1loop/internal/extensions"
)

// fakePlugins installs, in a temporary configuration folder, plugins with
// skills each, enabled, as an install leaves them; the user has no hooks.
func fakePlugins(tb testing.TB, plugins, skills int) string {
	tb.Helper()
	dir := tb.TempDir()
	tb.Setenv("XDG_CONFIG_HOME", dir) // Linux
	tb.Setenv("AppData", dir)         // Windows
	tb.Setenv("HOME", dir)            // macOS
	var registry extensions.Registry
	for p := range plugins {
		root := filepath.Join(extensions.Dir(), "plugins", fmt.Sprintf("owner-repo-%d", p))
		write := func(name, content string) {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				tb.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				tb.Fatal(err)
			}
		}
		write(".claude-plugin/plugin.json", fmt.Sprintf(`{"name":"plugin-%d","description":"A plugin","version":"1.0.0"}`, p))
		for s := range skills {
			write(fmt.Sprintf("skills/skill-%d/SKILL.md", s), fmt.Sprintf("---\nname: p%d-skill-%d\ndescription: >\n  What skill %d of plugin %d is for, folded\n  over two lines.\n---\n\n# Skill\n\n%s\n", p, s, s, p, strings.Repeat("Body text. ", 200)))
			write(fmt.Sprintf("skills/skill-%d/reference.md", s), "More.\n")
		}
		write("commands/run.md", "Run.\n")
		registry.Plugins = append(registry.Plugins, extensions.Plugin{ID: fmt.Sprintf("owner/repo-%d", p), Name: fmt.Sprintf("plugin-%d", p), Dir: root, Enabled: true, Installed: time.Now()})
	}
	if err := extensions.Update(func(current *extensions.Registry) error { *current = registry; return nil }); err != nil {
		tb.Fatal(err)
	}
	return dir
}

// BenchmarkToolCallHooks is one tool call's hooks, PreToolUse then
// PostToolUse with 64 KB of output, with no hooks and four plugins of eight
// skills each installed (#519).
func BenchmarkToolCallHooks(b *testing.B) {
	dir := fakePlugins(b, 4, 8)
	ctx := context.Background()
	input := map[string]any{"command": "go test ./..."}
	output := strings.Repeat("ok  \t1loop/internal/agent\t0.123s\n", 2000)
	// The files age past the stamps' racy window, as they do between installs.
	old := time.Now().Add(-time.Minute)
	filepath.Walk(dir, func(path string, _ os.FileInfo, _ error) error { return os.Chtimes(path, old, old) })
	if len(extensions.Enabled().Skills) != 32 {
		b.Fatalf("%d skills", len(extensions.Enabled().Skills))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		payload := func() map[string]any {
			return map[string]any{"session_id": "chat", "cwd": dir, "tool_name": "shell", "tool_input": input}
		}
		if result := runHooks(ctx, "PreToolUse", "shell", payload()); result.blocked || len(result.problems) > 0 {
			b.Fatal(result)
		}
		after := payload()
		after["tool_response"] = map[string]any{"output": output, "error": nil}
		if result := runHooks(ctx, "PostToolUse", "shell", after); result.blocked || len(result.problems) > 0 {
			b.Fatal(result)
		}
	}
}
