// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"sync"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
)

func (ui *desktopUI) loadAccountLimits(ctx context.Context, menu *sheet, conn agent.Connection, show func(agent.AccountLimits)) {
	if cached, ok := ui.accountLimits[conn.ID]; ok {
		show(cached)
		if time.Since(cached.Checked) < time.Minute {
			return
		}
	}
	if conn.Provider != "openai" && conn.Provider != "claudecode" && conn.Provider != "antigravity" && conn.Provider != "openrouter" {
		show(agent.AccountLimits{Note: "Limits not provided"})
		return
	}
	if _, ok := ui.accountLimits[conn.ID]; !ok {
		show(agent.AccountLimits{Note: "Loading limits…"})
	}
	key := ui.keys[conn.ID]
	go func() {
		limits, err := ui.backend.ConnectionLimits(ctx, conn, key)
		if err != nil {
			limits = agent.AccountLimits{Note: "Limits unavailable"}
		}
		limits.Checked = time.Now()
		if agent.SubscriptionLimits(conn.Provider) {
			ui.usage.NoteLimits(conn.ID, limits.UsageWindows(), limits.Checked) // the Usage page's pace
		}
		fyne.Do(func() {
			if ctx.Err() != nil || ui.model.open != menu || menu.closing {
				return
			}
			if ui.accountLimits == nil {
				ui.accountLimits = map[string]agent.AccountLimits{}
			}
			if len(limits.Windows) == 0 && len(ui.accountLimits[conn.ID].Windows) > 0 {
				show(ui.accountLimits[conn.ID])
				return
			}
			ui.accountLimits[conn.ID] = limits
			show(limits)
		})
	}()
}

// staleAccountLimits marks an account's limits out of date once a turn has
// used it: the next look at them asks the provider again, showing the old
// figures meanwhile, and nothing is fetched until someone looks.
func (ui *desktopUI) staleAccountLimits(connectionID string) {
	if limits, ok := ui.accountLimits[connectionID]; ok {
		limits.Checked = time.Time{}
		ui.accountLimits[connectionID] = limits
	}
}

// noteLimits reads a subscription's usage windows into the usage ledger, at
// most every interval (agent.App.NoteLimits): after a turn, so the Usage
// page's pace follows the use, and when the page opens. done runs on the
// interface's thread once something was read.
func (ui *desktopUI) noteLimits(connectionID string, every time.Duration, done func()) {
	var conn agent.Connection
	found := false
	for _, candidate := range ui.connectionsList {
		if candidate.ID == connectionID {
			conn, found = candidate, true
		}
	}
	if !found || !agent.SubscriptionLimits(conn.Provider) {
		return
	}
	key := ui.keys[conn.ID]
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 15*time.Second)
		defer cancel()
		limits, ok := ui.backend.NoteLimits(ctx, conn, key, ui.usage, every)
		if !ok {
			return
		}
		fyne.Do(func() {
			if ui.accountLimits == nil {
				ui.accountLimits = map[string]agent.AccountLimits{}
			}
			ui.accountLimits[conn.ID] = limits
			if done != nil {
				done()
			}
		})
	}()
}

// noteAllLimits reads every subscription's windows not read in the last
// two minutes, and waits for them: the phone's Usage screen then shows
// their pace as it is now. Never on the interface's thread.
func (ui *desktopUI) noteAllLimits(ctx context.Context) {
	var connections []agent.Connection
	keys := map[string]string{}
	fyne.DoAndWait(func() {
		for _, conn := range ui.connectionsList {
			if conn.Enabled && agent.SubscriptionLimits(conn.Provider) {
				connections = append(connections, conn)
				keys[conn.ID] = ui.keys[conn.ID]
			}
		}
	})
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	for _, conn := range connections {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if limits, ok := ui.backend.NoteLimits(ctx, conn, keys[conn.ID], ui.usage, 2*time.Minute); ok {
				fyne.Do(func() {
					if ui.accountLimits == nil {
						ui.accountLimits = map[string]agent.AccountLimits{}
					}
					ui.accountLimits[conn.ID] = limits
				})
			}
		}()
	}
	wait.Wait()
}
