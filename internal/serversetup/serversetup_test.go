// SPDX-License-Identifier: MPL-2.0

package serversetup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The four ways to run a server, each with steps to follow; every step says
// something, and its link opens an https page.
func TestMethods(t *testing.T) {
	var ids []string
	for _, method := range Methods() {
		ids = append(ids, method.ID)
		if method.Title == "" || method.Detail == "" || len(method.Steps) == 0 {
			t.Errorf("%s: a title, a detail and steps", method.ID)
		}
		for i, step := range method.Steps {
			if strings.TrimSpace(step.Text) == "" {
				t.Errorf("%s step %d has no text", method.ID, i+1)
			}
			if step.Link != nil && (step.Link.Label == "" || !strings.HasPrefix(step.Link.URL, "https://")) {
				t.Errorf("%s step %d: link %+v", method.ID, i+1, *step.Link)
			}
		}
	}
	if got := strings.Join(ids, ","); got != "docker,compose,linux,windows" {
		t.Fatalf("methods %s", got)
	}
}

// Each guide ends with approving this device as the pairing dialog asks:
// its command is a docker exec, run on the host, for a server in a
// container; the programs' consoles take its short "pair" line. Nothing is
// pasted into "docker attach", whose console takes only the short line.
func TestPairingStep(t *testing.T) {
	for _, method := range Methods() {
		last := method.Steps[len(method.Steps)-1]
		want := "line starting with pair"
		if method.ID == "docker" || method.ID == "compose" {
			want = "docker exec"
		}
		if !strings.Contains(last.Text, want) {
			t.Errorf("%s: the last step %q does not say %q", method.ID, last.Text, want)
		}
		for i, step := range method.Steps {
			if strings.Contains(step.Command, "docker attach") {
				t.Errorf("%s step %d still opens the console with docker attach", method.ID, i+1)
			}
		}
	}
}

// phoneCopy is the guides as the phone's build bundles them.
const phoneCopy = "../../android/app/src/main/assets/server_setup.json"

// TestPhoneCopy keeps the phone's copy in step; run with UPDATE_SETTINGS=1
// to write it.
func TestPhoneCopy(t *testing.T) {
	if os.Getenv("UPDATE_SETTINGS") != "" {
		if err := os.WriteFile(filepath.FromSlash(phoneCopy), JSON(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.FromSlash(phoneCopy))
	if err != nil {
		t.Fatalf("%v: run the test with UPDATE_SETTINGS=1", err)
	}
	if !bytes.Equal(got, JSON()) {
		t.Fatalf("%s is out of date: run go test ./internal/serversetup with UPDATE_SETTINGS=1", phoneCopy)
	}
}
