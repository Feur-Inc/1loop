// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/usage"
)

// An account kept but turned off has no limits on the Usage page; what it
// spent stays in the totals (#628).
func TestUsageHidesDisabledAccounts(t *testing.T) {
	now := time.Now()
	ui := &desktopUI{desktopApp: &desktopApp{usage: usage.Open(""), prices: usage.OpenPrices(""), connectionsList: []agent.Connection{
		{ID: "on", Name: "On", Enabled: true},
		{ID: "off", Name: "Off"},
	}}}
	for _, id := range []string{"on", "off"} {
		ui.usage.NoteLimits(id, []usage.LimitWindow{{Label: "7d", Left: 60, Reset: now.Add(72 * time.Hour)}}, now)
		ui.usage.Add(usage.Record{Time: now, Connection: id, Model: "m", Tokens: usage.Tokens{Input: 10}})
	}
	report, err := localUsage{ui}.report(context.Background(), usage.Query{Period: "all", Group: "connection"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Limits) != 1 || report.Limits[0].Connection != "on" {
		t.Errorf("limits %+v", report.Limits)
	}
	if len(report.Rows) != 2 || report.Totals.Calls != 2 {
		t.Errorf("%d rows, %d calls: the disabled account's spending is gone", len(report.Rows), report.Totals.Calls)
	}
	ui.connectionsList[1].Enabled = true
	if report, _ = (localUsage{ui}).report(context.Background(), usage.Query{Period: "all"}); len(report.Limits) != 2 {
		t.Errorf("enabled again, limits %+v", report.Limits)
	}
}
