// SPDX-License-Identifier: MPL-2.0

package platform

// The Flatpak's permissions as the sandbox really has them: the user can
// revoke any of them (flatpak override, Flatseal, the desktop's settings),
// and 1loop then says what no longer works instead of failing silently.

import (
	"bufio"
	"os"
	"strings"
)

// FlatpakID is the Flatpak's application id, used in the commands shown to
// restore a permission.
const FlatpakID = "io.github.Feur_Inc.OneLoop"

// flatpakInfo is where Flatpak describes the running sandbox, overrides
// included: a key file with [Context] and [Session Bus Policy] groups.
var flatpakInfo = "/.flatpak-info"

// FlatpakPermissionProblems lists, for each permission 1loop needs that the
// running Flatpak lacks, what stops working and how to restore it. Nothing
// outside the Flatpak (ONELOOP_FLATPAK, set by its launcher).
func FlatpakPermissionProblems() []string {
	if os.Getenv("ONELOOP_FLATPAK") == "" {
		return nil
	}
	file, err := os.Open(flatpakInfo)
	if err != nil {
		return nil
	}
	defer file.Close()
	var filesystems []string
	talksToFlatpak := false
	group := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			group = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch {
		case group == "[Context]" && key == "filesystems":
			filesystems = strings.Split(value, ";")
		case group == "[Session Bus Policy]" && key == "org.freedesktop.Flatpak":
			talksToFlatpak = value == "talk" || value == "own"
		}
	}
	if scanner.Err() != nil {
		return nil
	}
	var problems []string
	if !talksToFlatpak {
		problems = append(problems, "Commands, the terminal, Git, Browser Use and Computer Use cannot run: they run on the host through flatpak-spawn, "+
			"which needs the org.freedesktop.Flatpak permission. Restore it with:\n"+
			"flatpak override --user --talk-name=org.freedesktop.Flatpak "+FlatpakID)
	}
	switch hostAccess(filesystems) {
	case "":
		problems = append(problems, "1loop cannot see your files: only folders granted to it (and files picked in a file chooser) are visible, "+
			"so projects elsewhere cannot be opened or edited. Restore full access with:\n"+
			"flatpak override --user --filesystem=host "+FlatpakID)
	case "ro":
		problems = append(problems, "1loop can read your files but not change them: the agent's edits will fail. Restore write access with:\n"+
			"flatpak override --user --filesystem=host "+FlatpakID)
	}
	return problems
}

// hostAccess is "rw" when filesystems grant all of the user's files, "ro"
// when only to read, else "". home is enough for a project in the home
// folder, the usual place, so it counts as host.
func hostAccess(filesystems []string) string {
	access := ""
	for _, entry := range filesystems {
		name, mode, _ := strings.Cut(entry, ":")
		if name != "host" && name != "home" && name != "~" {
			continue
		}
		if mode == "ro" {
			if access == "" {
				access = "ro"
			}
			continue
		}
		access = "rw"
	}
	return access
}
