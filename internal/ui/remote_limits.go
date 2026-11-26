// SPDX-License-Identifier: MPL-2.0

package ui

// The server's account limits (GET /api/limits) as the server window's
// model menu shows them: kept between openings, so they show at once, and
// read again once they are a minute old or a turn has ended, as this
// computer's are (account_limits.go).

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

// remoteLimitsTooOld is what the menu says when the server has no
// /api/limits.
const remoteLimitsTooOld = "This server is too old to show usage limits: update it"

// remoteLimitsEvery is how old the server's limits get before the menu asks
// again (the server keeps its own for as long).
const remoteLimitsEvery = time.Minute

type remoteLimits struct {
	values  map[string]agent.AccountLimits // by account
	read    time.Time                      // the last answer; zero to ask again
	problem string                         // why the last read failed
}

// due says whether the limits should be read again.
func (limits *remoteLimits) due(now time.Time) bool {
	return limits.read.IsZero() || now.Sub(limits.read) >= remoteLimitsEvery
}

// apply keeps an answer of /api/limits, or why there was none. The last
// good numbers stay after a failed read, unless the server has no limits
// any more.
func (limits *remoteLimits) apply(accounts []remoteapi.Limits, err error, now time.Time) {
	limits.read = now
	if err != nil {
		limits.problem = remoteLimitsProblem(err)
		if limits.problem == remoteLimitsTooOld {
			limits.values = nil // a server put back to an older version: its numbers are gone
		}
		return
	}
	limits.problem = ""
	limits.values = map[string]agent.AccountLimits{}
	for _, account := range accounts {
		limits.values[account.Account] = accountLimitsOf(account, now)
	}
}

// noteTurns marks the limits out of date when a chat of the server stopped
// running: the turn used an account.
func (limits *remoteLimits) noteTurns(before, after []remoteapi.Chat) {
	for _, chat := range before {
		if chat.Running && slices.ContainsFunc(after, func(next remoteapi.Chat) bool { return next.ID == chat.ID && !next.Running }) {
			limits.read = time.Time{}
			return
		}
	}
}

// of is what the menu shows for an account.
func (limits *remoteLimits) of(account string) agent.AccountLimits {
	value, ok := limits.values[account]
	switch {
	case limits.problem != "" && len(value.Windows) > 0:
		value.Note = limits.problem // beside the last good numbers
		return value
	case limits.problem != "":
		return agent.AccountLimits{Note: limits.problem}
	case limits.read.IsZero() && !ok:
		return agent.AccountLimits{Note: "Loading limits…"}
	case len(value.Windows) == 0 && (value.Note == "" || value.Note == "Limits not provided"): // an older server's words
		value.Note = agent.NoLimitsNote
	}
	return value
}

// accountLimitsOf is an account's limits as the server sent them; read is
// their time when the server does not say it.
func accountLimitsOf(account remoteapi.Limits, read time.Time) agent.AccountLimits {
	value := agent.AccountLimits{Note: account.Note, Checked: read}
	if account.Checked > 0 {
		value.Checked = time.UnixMilli(account.Checked)
	}
	for _, window := range account.Windows {
		reset := time.Time{}
		if window.Reset > 0 {
			reset = time.UnixMilli(window.Reset)
		}
		value.Windows = append(value.Windows, agent.NewAccountLimitWindow(window.Label, window.Left, reset))
	}
	return value
}

// remoteLimitsProblem says why the server's limits could not be read.
func remoteLimitsProblem(err error) string {
	var refused remoteError
	if errors.As(err, &refused) && (refused.status == http.StatusNotFound || refused.status == http.StatusMethodNotAllowed) {
		return remoteLimitsTooOld
	}
	return "Couldn't read the server's limits: " + err.Error()
}

// watchLimits shows the server's limits while the menu is open: those
// kept at once, then read again when due and every minute. On the
// interface's thread; show runs there after each read.
func (remote *remoteDesktop) watchLimits(ctx context.Context, show func()) {
	read := remote.limits.due(time.Now())
	go func() {
		ticker := time.NewTicker(remoteLimitsEvery)
		defer ticker.Stop()
		for {
			if read {
				var answer struct {
					Accounts []remoteapi.Limits `json:"accounts"`
				}
				err := remote.client.json(ctx, "GET", "/api/limits", nil, &answer)
				if ctx.Err() != nil {
					return
				}
				fyne.Do(func() {
					remote.limits.apply(answer.Accounts, err, time.Now())
					if ctx.Err() == nil {
						show()
					}
				})
			}
			read = true
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
