// SPDX-License-Identifier: MPL-2.0

package server

import (
	"encoding/json"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// GET /api/limits: every enabled account, with its windows and when they
// were read, or why it has none.
func TestLimitsRoute(t *testing.T) {
	server, _, call := testServer(t)
	read := time.Now().Add(-20 * time.Second).Truncate(time.Millisecond)
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Millisecond)
	server.mu.Lock()
	server.connections = []agent.Connection{
		{ID: "plan", Name: "ChatGPT", Provider: "openai", Enabled: true},
		{ID: "local", Name: "Local", Provider: "compatible", BaseURL: "http://127.0.0.1:1/v1", Enabled: true},
		{ID: "off", Name: "Off", Provider: "openai"},
	}
	server.limitsCache = map[string]agent.AccountLimits{"plan": {
		Windows: []agent.AccountLimitWindow{agent.NewAccountLimitWindow("5h", 64, reset)},
		Note:    "Rate limited, try again in 5 min", Checked: read,
	}}
	server.mu.Unlock()

	code, data := call("GET", "/api/limits", nil)
	var answer struct{ Accounts []remoteapi.Limits }
	if code != 200 || json.Unmarshal(data, &answer) != nil {
		t.Fatalf("limits: %d %s", code, data)
	}
	if len(answer.Accounts) != 2 {
		t.Fatalf("the enabled accounts only: %s", data)
	}
	plan, local := answer.Accounts[0], answer.Accounts[1]
	if plan.Account != "plan" || len(plan.Windows) != 1 || plan.Windows[0].Left != 64 || plan.Windows[0].Reset != reset.UnixMilli() ||
		plan.Note != "Rate limited, try again in 5 min" || plan.Checked != read.UnixMilli() {
		t.Fatalf("the cached account: %+v", plan)
	}
	if local.Account != "local" || len(local.Windows) != 0 || local.Note != agent.NoLimitsNote || local.Checked == 0 {
		t.Fatalf("an account without limits says so: %+v", local)
	}
}
