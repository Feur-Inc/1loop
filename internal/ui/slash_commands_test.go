package ui

import (
	"testing"

	"1loop/internal/agent"
)

func TestCommandOptions(t *testing.T) {
	info := agent.ClaudeInfo{Commands: []agent.ClaudeCommand{
		{Name: "compact", Description: "Claude's compact"},
		{Name: "clear", Aliases: []string{"reset", "new"}},
		{Name: "usage", Description: "Show plan usage\nand more", Aliases: []string{"cost"}},
		{Name: "logout"},
		{Name: "my-command", ArgumentHint: "[file]"},
	}}
	options := commandOptions(info)
	seen := map[string]commandOption{}
	for _, option := range options {
		if _, twice := seen[option.name]; twice {
			t.Errorf("/%s listed twice", option.name)
		}
		seen[option.name] = option
	}
	if _, ok := seen["logout"]; ok {
		t.Error("/logout is offered but cannot work in 1loop")
	}
	if seen["usage"].about != "Show plan usage" || seen["my-command"].hint != "[file]" {
		t.Errorf("options = %+v", options)
	}
	if _, ok := seen["status"]; !ok {
		t.Error("/status, which 1loop runs, is missing")
	}
}

func TestMatchCommands(t *testing.T) {
	options := []commandOption{{name: "status"}, {name: "usage"}, {name: "security-review"}, {name: "simplify"}}
	got := matchCommands(options, "s")
	if len(got) != 4 || got[0].name != "status" || got[1].name != "security-review" || got[2].name != "simplify" || got[3].name != "usage" {
		t.Errorf("matches for s = %+v", got)
	}
	if got := matchCommands(options, "rev"); len(got) != 1 || got[0].name != "security-review" {
		t.Errorf("matches for rev = %+v", got)
	}
	if got := matchCommands(options, ""); len(got) != len(options) {
		t.Errorf("all commands for /: %+v", got)
	}
}
