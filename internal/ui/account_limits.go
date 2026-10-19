package ui

import (
	"context"
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
	if conn.Provider != "openai" && conn.Provider != "claudecode" && conn.Provider != "openrouter" {
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
