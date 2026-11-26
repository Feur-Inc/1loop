// SPDX-License-Identifier: MPL-2.0

package extensions

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"1loop/internal/core"
)

// enabledReads points the extensions folder at a temporary one with an
// enabled plugin (two skills, an MCP server) and counts how often Enabled
// reads it.
func enabledReads(t *testing.T) (plugin string, reads func() int) {
	t.Helper()
	dir := t.TempDir()
	previous := dirFunc
	dirFunc = func() string { return dir }
	t.Cleanup(func() { dirFunc = previous })
	var mu sync.Mutex
	count := 0
	readEnabled = func(stamps *core.FileStamps) Active {
		mu.Lock()
		count++
		mu.Unlock()
		return readActive(stamps)
	}
	t.Cleanup(func() { readEnabled = readActive })
	plugin = filepath.Join(dir, "plugins", "owner-repo")
	writeFile(t, filepath.Join(plugin, ".claude-plugin", "plugin.json"), `{"name":"repo"}`)
	writeFile(t, filepath.Join(plugin, "skills", "alpha", "SKILL.md"), "---\nname: alpha\ndescription: First\n---\n")
	writeFile(t, filepath.Join(plugin, "skills", "beta", "SKILL.md"), "---\nname: beta\ndescription: Second\n---\n")
	writeFile(t, filepath.Join(plugin, ".mcp.json"), `{"mcpServers":{"tool":{"command":"run","args":["a"],"env":{"K":"v"}}}}`)
	if err := Update(func(registry *Registry) error {
		registry.Plugins = []Plugin{{ID: "owner/repo", Name: "repo", Dir: plugin, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return plugin, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// age puts every file and folder under dir a minute in the past, beyond
// the stamps' margin, as files are between installs.
func age(t *testing.T, dir string) {
	t.Helper()
	old := time.Now().Add(-time.Minute)
	filepath.Walk(dir, func(path string, _ os.FileInfo, err error) error {
		if err == nil {
			err = os.Chtimes(path, old, old)
		}
		return err
	})
}

func skillNames(active Active) []string {
	var names []string
	for _, skill := range active.Skills {
		names = append(names, skill.Description)
	}
	return names
}

// Enabled reads the folder once, then only when something in it changed
// (#519), and its callers cannot change what it keeps.
func TestEnabledIsKept(t *testing.T) {
	plugin, reads := enabledReads(t)
	age(t, Dir())
	first := Enabled()
	if len(first.Skills) != 2 || len(first.MCP) != 1 || reads() != 1 {
		t.Fatalf("%d skills, %d servers, %d reads", len(first.Skills), len(first.MCP), reads())
	}
	first.Skills[0].Description = "changed"
	first.MCP[0].Env["K"] = "changed"
	first.MCP[0].Args[0] = "changed"
	again := Enabled()
	if reads() != 1 {
		t.Fatalf("read again with nothing changed: %d reads", reads())
	}
	if again.Skills[0].Description != "First" || again.MCP[0].Env["K"] != "v" || again.MCP[0].Args[0] != "a" {
		t.Fatalf("a caller changed the cache: %+v %+v", again.Skills[0], again.MCP[0])
	}

	// A skill's SKILL.md edited, then a skill added.
	writeFile(t, filepath.Join(plugin, "skills", "alpha", "SKILL.md"), "---\nname: alpha\ndescription: Edited\n---\n")
	if got := skillNames(Enabled()); len(got) != 2 || got[0] != "Edited" {
		t.Fatalf("edit not seen: %v", got)
	}
	writeFile(t, filepath.Join(plugin, "skills", "gamma", "SKILL.md"), "---\nname: gamma\ndescription: Third\n---\n")
	if got := skillNames(Enabled()); len(got) != 3 {
		t.Fatalf("new skill not seen: %v", got)
	}
	// Just changed, the files are read at each call until they are older.
	before := reads()
	Enabled()
	if reads() != before+1 {
		t.Fatal("a file changed within the margin was trusted")
	}
	age(t, Dir())
	Enabled()
	before = reads()
	Enabled()
	if reads() != before {
		t.Fatal("not kept once the files are older")
	}

	// A hooks file added to the plugin, then its MCP servers removed.
	writeFile(t, filepath.Join(plugin, "hooks", "hooks.json"), `{"hooks":{}}`)
	if hooks := Enabled().Hooks; len(hooks) != 1 || hooks[0].Root != plugin {
		t.Fatalf("hooks: %+v", hooks)
	}
	os.Remove(filepath.Join(plugin, ".mcp.json"))
	if servers := Enabled().MCP; len(servers) != 0 {
		t.Fatalf("servers: %+v", servers)
	}
}

// Turning a plugin off or on through the package applies at once, and
// so does any write of the registry, even one its stamp cannot tell (#519).
func TestEnabledFollowsSwitches(t *testing.T) {
	_, reads := enabledReads(t)
	age(t, Dir())
	if len(Enabled().Skills) != 2 {
		t.Fatal("plugin's skills missing")
	}
	if err := SetEnabled("plugin", "owner/repo", false); err != nil {
		t.Fatal(err)
	}
	if active := Enabled(); len(active.Skills) != 0 || len(active.Plugins) != 0 {
		t.Fatalf("disabled plugin still active: %+v", active)
	}
	if err := SetEnabled("plugin", "owner/repo", true); err != nil {
		t.Fatal(err)
	}
	if len(Enabled().Skills) != 2 {
		t.Fatal("enabled plugin not active")
	}

	// The same size and time as before the write: only the write tells.
	old := time.Now().Add(-time.Minute)
	os.Chtimes(registryPath(), old, old)
	Enabled()
	before := reads()
	Enabled()
	if reads() != before {
		t.Fatal("read again with nothing changed")
	}
	if err := Update(func(*Registry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(registryPath(), old, old)
	Enabled()
	if reads() != before+1 {
		t.Fatal("a write of the registry did not make Enabled read again")
	}
}

// Enabled from many goroutines at once, while the registry changes (run
// with -race).
func TestEnabledInParallel(t *testing.T) {
	enabledReads(t)
	var wait sync.WaitGroup
	for i := range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := range 50 {
				active := Enabled()
				for k := range active.MCP {
					active.MCP[k].Env["K"] = "mine"
				}
				if i == 0 && j%10 == 0 {
					SetEnabled("plugin", "owner/repo", j%20 == 0)
				}
			}
		}()
	}
	wait.Wait()
}
