// SPDX-License-Identifier: MPL-2.0

package agent

// Every model call's tokens go to the usage ledger (internal/usage), tagged
// with the chat, turn, project and branch they were spent on: the Usage
// page's record of where tokens and money went.

import (
	"1loop/internal/core"
	"1loop/internal/usage"
)

// UsageTag is where a chat's model calls are kept: the host's ledger, and
// the chat (a subchat's parent too) they belong to.
type UsageTag struct {
	Ledger       *usage.Ledger
	Chat, Parent string
	turn         string
}

// spentTokens is what one call used, as its provider reported it.
type spentTokens struct {
	model  string // the model that answered, "" for the one asked for
	tokens usage.Tokens
	cost   *float64 // the provider's own price for it
}

// withTurn gives a run's calls their turn.
func withTurn(cfg Config) Config {
	if cfg.Usage != nil {
		tag := *cfg.Usage
		tag.turn = core.Identifier()[:12]
		cfg.Usage = &tag
	}
	return cfg
}

func recordUsage(cfg Config, spent spentTokens) {
	tag := cfg.Usage
	if tag == nil || tag.Ledger == nil {
		return
	}
	model := spent.model
	if model == "" {
		model = cfg.Model
	}
	record := usage.Record{Connection: cfg.ConnectionID, Provider: cfg.Provider, Model: model,
		Chat: tag.Chat, Parent: tag.Parent, Turn: tag.turn, Tokens: spent.tokens, Cost: spent.cost}
	if cfg.Scratch == "" || cfg.Scratch != cfg.Workspace {
		record.Project = cfg.Workspace
	}
	if cfg.MachineID == "" {
		record.Branch = usage.Branch(record.Project)
	}
	tag.Ledger.Add(record)
}

// callTally keeps the calls of an agent that reports them piecemeal:
// Claude Code repeats a message's usage on each of its blocks and sends the
// final output count apart. The largest counts seen for a message are kept
// until another message starts, the turn ends or the run does.
type callTally struct {
	cfg   Config
	id    string
	spent *spentTokens
}

func (tally *callTally) see(id, model string, input, output, cacheRead, cacheWrite int) {
	if id == "" {
		return
	}
	if id != tally.id || tally.spent == nil {
		tally.flush()
		tally.id, tally.spent = id, &spentTokens{}
	}
	if model != "" {
		tally.spent.model = model
	}
	tokens := &tally.spent.tokens
	tokens.Input = max(tokens.Input, input)
	tokens.Output = max(tokens.Output, output)
	tokens.CacheRead = max(tokens.CacheRead, cacheRead)
	tokens.CacheWrite = max(tokens.CacheWrite, cacheWrite)
}

func (tally *callTally) flush() {
	if tally.spent != nil {
		recordUsage(tally.cfg, *tally.spent)
		tally.spent = nil
	}
}

func usageTokens(input, output, cacheRead int) usage.Tokens {
	return usage.Tokens{Input: input, Output: output, CacheRead: cacheRead}
}
