// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"1loop/internal/settings"
)

// fakeSettings is a 1loop with a few settings, the protected ones among
// them: the tool must not reach those even when the store would answer.
func fakeSettings() (map[string]string, *SettingsAccess) {
	stored := map[string]string{
		settings.KeyDynamicCompaction: "on",
		settings.KeyMemory:            "on",
		settings.KeyWorktreeSort:      "recent",
		settings.KeyApprovalMode:      "approval",
	}
	return stored, &SettingsAccess{
		Get: func(_ context.Context, key string) (string, bool) { value, ok := stored[key]; return value, ok },
		Set: func(_ context.Context, key, value string) error { stored[key] = value; return nil },
	}
}

// runSettings calls the tool in YOLO mode, answering its approval with
// allow, and returns what the user was asked.
func runSettings(t *testing.T, access *SettingsAccess, allow bool, arguments map[string]string) (output string, err error, asked []string) {
	t.Helper()
	ctx := context.WithValue(context.Background(), approvalContextKey{}, approvalPolicy{yolo: true})
	ctx = context.WithValue(ctx, configContextKey{}, Config{Settings: access})
	for _, field := range []string{"key", "value"} {
		if _, ok := arguments[field]; !ok {
			arguments[field] = ""
		}
	}
	raw, _ := json.Marshal(arguments)
	call := ToolCall{ID: "call"}
	call.Function.Name, call.Function.Arguments = "settings", string(raw)
	backend := &App{Approvals: map[string]chan bool{}}
	output, _, err = backend.runTool(ctx, t.TempDir(), call, func() {}, func(event string, value any) {
		if event == "approval" {
			request := value.(map[string]any)
			asked = append(asked, request["title"].(string)+"\n"+request["preview"].(string))
			backend.Approve(request["id"].(string), allow)
		}
	})
	return output, err, asked
}

func TestSettingsTool(t *testing.T) {
	if (Config{}).toolAvailable("settings") {
		t.Fatal("settings is offered without a store")
	}
	stored, access := fakeSettings()
	if !(Config{Settings: access}).toolAvailable("settings") {
		t.Fatal("settings is not offered with a store")
	}

	// get lists what the model may change, never the security settings.
	output, err, _ := runSettings(t, access, true, map[string]string{"action": "get"})
	if err != nil || !strings.Contains(output, settings.KeyDynamicCompaction+": Dynamic compaction") || !strings.Contains(output, "allowed: recent (Latest commit), name (Name), pull (Pull requests first)") {
		t.Fatalf("get = %q, %v", output, err)
	}
	if strings.Contains(output, settings.KeyApprovalMode) {
		t.Fatalf("get shows the approval mode: %q", output)
	}

	// A change is asked for, even in YOLO mode, with the old and new value.
	output, err, asked := runSettings(t, access, true, map[string]string{"action": "set", "key": settings.KeyWorktreeSort, "value": "Name"})
	if err != nil || stored[settings.KeyWorktreeSort] != "name" || !strings.Contains(output, "from Latest commit to Name") {
		t.Fatalf("set = %q, %v; stored %q", output, err, stored[settings.KeyWorktreeSort])
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "Worktree order") || !strings.Contains(asked[0], "From: Latest commit\nTo:   Name") {
		t.Fatalf("the user was asked %q", asked)
	}

	// Declined: nothing changes.
	if _, err, asked = runSettings(t, access, false, map[string]string{"action": "set", "key": settings.KeyDynamicCompaction, "value": "off"}); err == nil || len(asked) != 1 || stored[settings.KeyDynamicCompaction] != "on" {
		t.Fatalf("a declined change: %v, asked %d times, stored %q", err, len(asked), stored[settings.KeyDynamicCompaction])
	}

	// Refused before anyone is asked.
	for _, refused := range []map[string]string{
		{"action": "set", "key": settings.KeyApprovalMode, "value": "yolo"},           // security
		{"action": "get", "key": settings.KeyApprovalMode},                            // not even readable
		{"action": "set", "key": "models.connections", "value": "on"},                 // accounts
		{"action": "set", "key": "phone.remote", "value": "on"},                       // sharing
		{"action": "set", "key": "agent.preferences", "value": "x"},                   // the memory tool's
		{"action": "set", "key": "nope", "value": "on"},                               // unknown
		{"action": "set", "key": settings.KeyAutoResume, "value": "on"},               // not in this 1loop
		{"action": "set", "key": settings.KeyDynamicCompaction, "value": "sometimes"}, // not a switch's value
		{"action": "set", "key": settings.KeyWorktreeSort, "value": "size"},           // not a choice
		{"action": "set", "key": "", "value": "on"},
		{"action": "reset", "key": settings.KeyMemory},
	} {
		if output, err, asked = runSettings(t, access, true, refused); err == nil || len(asked) > 0 {
			t.Errorf("%v: %q, %v, asked %q", refused, output, err, asked)
		}
	}
	if stored[settings.KeyApprovalMode] != "approval" || stored[settings.KeyDynamicCompaction] != "on" {
		t.Fatalf("a refused change was made: %v", stored)
	}

	// A value already set asks nothing; true is on.
	if output, err, asked = runSettings(t, access, true, map[string]string{"action": "set", "key": settings.KeyMemory, "value": "true"}); err != nil || len(asked) > 0 || !strings.Contains(output, "already") {
		t.Fatalf("an unchanged value: %q, %v, asked %q", output, err, asked)
	}
}

func TestDescribeSettings(t *testing.T) {
	for _, test := range []struct {
		args         string
		running      bool
		verb, target string
	}{
		{`{"action":"get","key":""}`, false, "Read", "1loop's settings"},
		{`{"action":"set","key":"agent.memory"}`, true, "Changing", "Memory"},
		{`{"action":"set","key":"general.worktree_sort"}`, false, "Changed", "Worktree order"},
	} {
		verb, target, _ := DescribeTool(ChatItem{Tool: "settings", Args: test.args}, test.running)
		if verb != test.verb || target != test.target {
			t.Errorf("DescribeTool(%s) = %q %q, want %q %q", test.args, verb, target, test.verb, test.target)
		}
	}
}
