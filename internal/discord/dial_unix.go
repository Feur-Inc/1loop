// SPDX-License-Identifier: MPL-2.0

//go:build !windows && !js

package discord

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// dial connects to the first Discord socket found: discord-ipc-0 to 9 in the
// runtime and temporary folders, and in the folders of the Flatpak and
// Snap builds of Discord and Vesktop.
func dial() (io.ReadWriteCloser, error) {
	for _, path := range socketPaths() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if conn, err := net.DialTimeout("unix", path, 1500*time.Millisecond); err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("Discord is not running")
}

func socketPaths() []string {
	var bases []string
	for _, name := range []string{"XDG_RUNTIME_DIR", "TMPDIR", "TMP", "TEMP"} {
		if dir := os.Getenv(name); dir != "" {
			bases = append(bases, dir)
		}
	}
	bases = append(bases, "/run/user/"+strconv.Itoa(os.Getuid()), "/tmp")
	subdirs := []string{"", "app/com.discordapp.Discord", "app/com.discordapp.DiscordCanary",
		"app/dev.vencord.Vesktop", ".flatpak/dev.vencord.Vesktop/xdg-run", "snap.discord", "snap.discord-canary"}
	seen := map[string]bool{}
	var paths []string
	for _, base := range bases {
		for _, subdir := range subdirs {
			dir := filepath.Join(base, subdir)
			if seen[dir] {
				continue
			}
			seen[dir] = true
			for i := 0; i < 10; i++ {
				paths = append(paths, filepath.Join(dir, "discord-ipc-"+strconv.Itoa(i)))
			}
		}
	}
	return paths
}
