// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
		"KeyMemory": registry.KeyMemory, "KeyAutoResume": registry.KeyAutoResume, "KeyWorktreeSort": registry.KeyWorktreeSort}
	marked := map[string]bool{}
	for _, match := range regexp.MustCompile(`\.mark\((?:"([^"]+)"|registry\.(\w+))`).FindAllStringSubmatch(source.String(), -1) {
		if match[1] != "" {
			marked[match[1]] = true
		} else {
			marked[constants[match[2]]] = true
		}
	}
	for _, setting := range registry.On(registry.OnDesktop) {
		if !marked[setting.Key] {
			t.Errorf("%s (%s) has no marked row on the desktop", setting.Key, setting.Title)
		}
	}
}
