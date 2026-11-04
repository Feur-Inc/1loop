// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlatpakPermissionProblems(t *testing.T) {
	t.Setenv("ONELOOP_FLATPAK", "1")
	cases := []struct {
		name, context, bus string
		want               []string // a word of each problem, in order
	}{
		{"all granted", "filesystems=host;", "org.freedesktop.Flatpak=talk", nil},
		{"home is enough", "filesystems=home;", "org.freedesktop.Flatpak=talk", nil},
		{"no host commands", "filesystems=host;", "org.freedesktop.Notifications=talk", []string{"--talk-name"}},
		{"no files", "filesystems=", "org.freedesktop.Flatpak=talk", []string{"cannot see"}},
		{"read only", "filesystems=host:ro;xdg-download;", "org.freedesktop.Flatpak=talk", []string{"not change"}},
		{"both revoked", "filesystems=xdg-documents;", "", []string{"--talk-name", "cannot see"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "flatpak-info")
			content := "[Application]\nname=" + FlatpakID + "\n\n[Context]\nshared=ipc;network;\n" + c.context + "\n\n[Session Bus Policy]\n" + c.bus + "\n"
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			flatpakInfo = path
			defer func() { flatpakInfo = "/.flatpak-info" }()
			problems := FlatpakPermissionProblems()
			if len(problems) != len(c.want) {
				t.Fatalf("got %q, want %d problems", problems, len(c.want))
			}
			for i, word := range c.want {
				if !strings.Contains(problems[i], word) {
					t.Errorf("problem %d = %q, want it to mention %q", i, problems[i], word)
				}
			}
		})
	}
}

func TestFlatpakPermissionProblemsOutsideFlatpak(t *testing.T) {
	t.Setenv("ONELOOP_FLATPAK", "")
	if problems := FlatpakPermissionProblems(); problems != nil {
		t.Fatalf("got %q outside the Flatpak", problems)
	}
}
