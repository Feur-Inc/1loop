package agent

// What only the desktop provides to the agent, registered by the desktop at
// start. The headless server registers nothing: no Computer Use, and its
// terminal captures work without a visible chat.

import "encoding/json"

type computerContextKey struct{}

var ComputerUse struct {
	ToolDefinition func() map[string]any
	Instructions   func(Config) string
	NeedsApproval  func(json.RawMessage) bool
}

func computerToolDefinition() map[string]any {
	if ComputerUse.ToolDefinition == nil {
		return nil
	}
	return ComputerUse.ToolDefinition()
}

func computerInstructions(cfg Config) string {
	if ComputerUse.Instructions == nil {
		return ""
	}
	return ComputerUse.Instructions(cfg)
}

func computerNeedsApproval(arguments json.RawMessage) bool {
	if ComputerUse.NeedsApproval == nil {
		return true
	}
	return ComputerUse.NeedsApproval(arguments)
}

var TerminalScreenVisibility = "On the headless server, captures work even without an attached or visible client. Text output is also readable in background chats."
