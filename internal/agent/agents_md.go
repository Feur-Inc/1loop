// SPDX-License-Identifier: MPL-2.0

package agent

// AGENTS.md: the project's instructions for coding agents, read from the
// workspace's root as Codex reads it (32 KiB at most). 1loop's loop puts it
// in its instructions; Claude Code receives it through its official
// --append-system-prompt argument.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
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

// ProjectMemory is the workspace's AGENTS.md, "" when it has none, for the
// settings to show and edit: the agent writes it with its file tools.
func ProjectMemory(workspace string) (string, error) {
	path, err := WorkspacePath(workspace, "AGENTS.md")
	if err != nil {
		return "", err
	}
	text, err := fileText(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return text, err
}

// SaveProjectMemory writes the workspace's AGENTS.md, refused when it
// changed since before was read.
func SaveProjectMemory(workspace, before, after string) error {
	path, err := WorkspacePath(workspace, "AGENTS.md")
	if err != nil {
		return err
	}
	if current, err := ProjectMemory(workspace); err != nil || current != before {
		return fmt.Errorf("AGENTS.md changed since it was read; read it again")
	}
	flags := os.O_WRONLY | os.O_TRUNC
	if _, err := os.Stat(path); os.IsNotExist(err) {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(after)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	return writeErr
}

// ProjectMemoryExists says whether the workspace has an AGENTS.md yet.
func ProjectMemoryExists(workspace string) bool {
	path, err := WorkspacePath(workspace, "AGENTS.md")
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// MachineProjectMemory is ProjectMemory for a workspace on an SSH machine,
// with whether the file exists there.
func MachineProjectMemory(ctx context.Context, machine Machine, workspace string) (string, bool, error) {
	name := path.Join(workspace, "AGENTS.md")
	if !machineExists(ctx, machine, name) {
		return "", false, nil
	}
	text, err := machineText(ctx, machine, name)
	return text, true, err
}

// SaveMachineProjectMemory is SaveProjectMemory on an SSH machine.
func SaveMachineProjectMemory(ctx context.Context, machine Machine, workspace, before, after string) error {
	current, exists, err := MachineProjectMemory(ctx, machine, workspace)
	if err != nil || current != before {
		return fmt.Errorf("AGENTS.md changed since it was read; read it again")
	}
	return machine.WriteFile(ctx, path.Join(workspace, "AGENTS.md"), []byte(after), !exists)
}
