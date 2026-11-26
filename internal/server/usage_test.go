// SPDX-License-Identifier: MPL-2.0

package server

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/usage"
)

// The Usage page of a server does not show the limits of an account kept
// but turned off; they come back with it (#628).
func TestUsageHidesDisabledAccounts(t *testing.T) {
	server, call := pairedServer(t)
	now := time.Now()
	server.connections = []agent.Connection{
		{ID: "on", Name: "On", Provider: "compatible", Enabled: true},
		{ID: "off", Name: "Off", Provider: "compatible"},
	}
	for _, id := range []string{"on", "off"} {
		server.usage.NoteLimits(id, []usage.LimitWindow{{Label: "7d", Left: 60, Reset: now.Add(72 * time.Hour)}}, now)
	}
	shown := func() (names []string) {
		t.Helper()
		code, body := call("GET", "/api/usage?period=month&group=model", nil, true)
		var report usage.Report
		if err := json.Unmarshal([]byte(body), &report); code != 200 || err != nil {
			t.Fatal(code, body, err)
		}
		for _, limit := range report.Limits {
			names = append(names, limit.Name)
		}
		return names
	}
	if got := shown(); !slices.Equal(got, []string{"On"}) {
		t.Errorf("limits of %v", got)
	}
	server.connections[1].Enabled = true
	if got := shown(); !slices.Equal(got, []string{"Off", "On"}) {
		t.Errorf("enabled again, limits of %v", got)
	}
}
