// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Windows GUI build has no stderr: the log file must still get each line.
func TestDevLogWithoutStderr(t *testing.T) {
	stderr, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	stderr.Close() // writes now fail, as on a stderr that does not exist
	path := filepath.Join(t.TempDir(), "dev.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	previous := Dev
	defer func() { Dev = previous }()
	Dev = &devLog{out: everyWriter{stderr, file}, path: path}
	Devf("app", "first line")
	Devf("tool", "second line")
	data, _ := os.ReadFile(path)
	if lines := strings.Count(string(data), "\n"); lines != 2 {
		t.Fatalf("the log holds %d lines, want 2:\n%s", lines, data)
	}
}
