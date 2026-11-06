// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestModelBrowserRefusesLocalFiles(t *testing.T) {
	var reached []string
	tools := modelBrowser(func(_ context.Context, name string, raw json.RawMessage) (PanelToolResult, error) {
		reached = append(reached, name+" "+string(raw))
		return PanelToolResult{}, nil
	})
	call := func(name, args string) error {
		_, err := tools(context.Background(), name, json.RawMessage(args))
		return err
	}
	for _, args := range []string{
		`{"action":"navigate","url":"/etc/hostname"}`,
		`{"action":"navigate","url":"file:///etc/hostname"}`,
		`{"action":"navigate","url":"FILE:///home/user/.ssh/id_ed25519"}`,
		`{"action":"navigate","url":"view-source:file:///etc/hostname"}`,
		`{"action":"navigate","url":"chrome://version"}`,
	} {
		if call("browser_navigate", args) == nil {
			t.Errorf("navigate %s was allowed", args)
		}
	}
	if call("browser_tabs", `{"action":"open","url":"/etc/hostname"}`) == nil {
		t.Error("a tab opened on a local file")
	}
	if len(reached) != 0 {
		t.Fatalf("refused calls reached the browser: %v", reached)
	}
	for _, c := range [][2]string{
		{"browser_navigate", `{"action":"navigate","url":"https://example.com"}`},
		{"browser_navigate", `{"action":"navigate","url":"localhost:8080"}`},
		{"browser_navigate", `{"action":"reload"}`},
		{"browser_tabs", `{"action":"open","url":""}`},
		{"browser_tabs", `{"action":"open","url":"data:text/html,hi"}`},
		{"browser_read", `{"tab_id":"x"}`},
	} {
		if err := call(c[0], c[1]); err != nil {
			t.Errorf("%s %s: %v", c[0], c[1], err)
		}
	}
	if len(reached) != 6 {
		t.Fatalf("allowed calls did not all reach the browser: %v", reached)
	}
	if modelBrowser(nil) != nil {
		t.Error("no browser must stay no browser")
	}
}
