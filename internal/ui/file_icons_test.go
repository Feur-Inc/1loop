// SPDX-License-Identifier: MPL-2.0

package ui

import "testing"

func TestFileIconKind(t *testing.T) {
	cases := []struct {
		name string
		dir  bool
		want string
	}{
		{".github", true, "folder-github"},
		{"docs", true, "folder-docs"},
		{"assets", true, "folder-assets"},
		{"android", true, "folder"},
		{"go.mod", false, "go"},
		{"main.go", false, "go"},
		{"MainActivity.kt", false, "kotlin"},
		{"build.gradle.kts", false, "kotlin"},
		{"release.sh", false, "shell"},
		{"README.md", false, "markdown"},
		{"package.json", false, "json"},
		{"ci.yml", false, "yaml"},
		{"Cargo.toml", false, "toml"},
		{"flake.nix", false, "nix"},
		{"Dockerfile", false, "docker"},
		{"Dockerfile.dev", false, "docker"},
		{"Makefile", false, "make"},
		{"logo.PNG", false, "image"},
		{"icon.svg", false, "image"},
		{"dist.tar.gz", false, "archive"},
		{"LICENSE", false, "license"},
		{"COPYING.txt", false, "license"},
		{"go.sum", false, "lock"},
		{"flake.lock", false, "lock"},
		{"package-lock.json", false, "lock"},
		{".gitignore", false, "dotfile"},
		{"notes.txt", false, "text"},
		{"unknown.xyz", false, "file"},
		{"noext", false, "file"},
	}
	for _, c := range cases {
		if got := fileIconKind(c.name, c.dir); got != c.want {
			t.Errorf("fileIconKind(%q, %v) = %q, want %q", c.name, c.dir, got, c.want)
		}
	}
	for _, c := range cases {
		if fileTypeIcon(c.name, c.dir) == nil {
			t.Errorf("no icon for %q", c.name)
		}
	}
}
