package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"1loop/internal/platform"
)

// The test binary is the sandbox helper too, as 1loop is.
func TestMain(m *testing.M) {
	platform.SandboxMain()
	os.Exit(m.Run())
}

// autoShell runs the shell tool in Auto mode with a reviewer that records
// what it was asked and allows it.
func autoShell(t *testing.T, workspace string, arguments map[string]any) (output string, err error, reviewed []string) {
	t.Helper()
	review := func(_ context.Context, _ emitter, title, preview string) (bool, string) {
		reviewed = append(reviewed, title+" "+preview)
		return true, "test reviewer"
	}
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{review: review})
	raw, _ := json.Marshal(arguments)
	call := ToolCall{ID: "call"}
	call.Function.Name, call.Function.Arguments = "shell", string(raw)
	output, _, err = (&App{Approvals: map[string]chan bool{}}).runTool(ctx, workspace, call, func() {}, func(string, any) {})
	return output, err, reviewed
}

func TestAutoModeShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash commands")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := func(text string) map[string]any {
		return map[string]any{"command": text, "max_output_tokens": nil, "escalate": nil, "justification": nil}
	}

	// A plain read: no review.
	if output, err, reviewed := autoShell(t, workspace, command("cat notes.txt")); err != nil || len(reviewed) != 0 || !strings.Contains(output, "hello") {
		t.Fatalf("read: %v %q reviewed %v", err, output, reviewed)
	}
	// A deletion: always reviewed, then run.
	if _, err, reviewed := autoShell(t, workspace, command("rm notes.txt")); err != nil || len(reviewed) != 1 {
		t.Fatalf("rm: %v reviewed %v", err, reviewed)
	}
	if _, err := os.Stat(filepath.Join(workspace, "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("the reviewed rm did not run")
	}
	// Escalation needs a reason, then goes to the reviewer.
	escalated := command("ls")
	escalated["escalate"] = true
	if _, err, reviewed := autoShell(t, workspace, escalated); err == nil || len(reviewed) != 0 {
		t.Fatalf("escalation without a justification: %v reviewed %v", err, reviewed)
	}
	escalated["justification"] = "needs the home folder"
	if _, err, reviewed := autoShell(t, workspace, escalated); err != nil || len(reviewed) != 1 || !strings.HasPrefix(reviewed[0], "Run outside the sandbox?") || !strings.Contains(reviewed[0], "needs the home folder") {
		t.Fatalf("escalation: %v reviewed %v", err, reviewed)
	}

	if !platform.SandboxAvailable() || os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		t.Skip("no Landlock: ordinary commands go to the reviewer here")
	}
	// Ordinary work: in the sandbox, without a review.
	if output, err, reviewed := autoShell(t, workspace, command("mkdir -p src && echo made > src/a.txt && cat src/a.txt")); err != nil || len(reviewed) != 0 || !strings.Contains(output, "made") {
		t.Fatalf("sandboxed work: %v %q reviewed %v", err, output, reviewed)
	}
	// Outside the workspace the sandbox refuses, and says how to escalate.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	escape := filepath.Join(home, ".1loop-auto-test")
	output, err, reviewed := autoShell(t, workspace, command("echo no > "+escape))
	if _, statErr := os.Stat(escape); statErr == nil {
		os.Remove(escape)
		t.Fatal("the sandbox let a command write in the home folder")
	}
	if err == nil || len(reviewed) != 0 || !strings.Contains(output, "escalate: true") {
		t.Fatalf("sandbox refusal: %v %q reviewed %v", err, output, reviewed)
	}
}
