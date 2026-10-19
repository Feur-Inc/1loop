package agent

// AGENTS.md: the project's instructions for coding agents, read from the
// workspace's root as Codex reads it (32 KiB at most). 1loop's loop puts it
// in its instructions; Claude Code receives it through its official
// --append-system-prompt argument.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

const agentsFileLimit = 32 << 10

// projectInstructions is the workspace's AGENTS.md, or "".
func projectInstructions(workspace string) string {
	if workspace == "" {
		return ""
	}
	path := filepath.Join(workspace, "AGENTS.md")
	if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		var err error
		path, err = WorkspacePath(workspace, path)
		if err != nil {
			return ""
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, agentsFileLimit+1))
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return ""
	}
	text := strings.ToValidUTF8(string(data[:min(len(data), agentsFileLimit)]), "")
	if len(data) > agentsFileLimit {
		text += "\n[AGENTS.md is longer than 32 KiB; the rest was left out.]"
	}
	return "\n\nProject instructions from AGENTS.md (the user's rules for this workspace):\n" + text
}
