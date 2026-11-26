// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// A process 1loop starts, and its own children, are the out-of-memory
// killer's first pick (#584).
func TestOOMFirst(t *testing.T) {
	if _, err := os.Stat("/proc/self/oom_score_adj"); err != nil {
		t.Skip("no oom_score_adj")
	}
	cmd := exec.Command("sh", "-c", "read line; sh -c 'cat /proc/$$/oom_score_adj'")
	stdin, _ := cmd.StdinPipe()
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	OOMFirst(cmd)
	stdin.Write([]byte("go\n"))
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != strconv.Itoa(ChildOOMScoreAdj) {
		t.Fatalf("the child's child has oom_score_adj %s", got)
	}
}
