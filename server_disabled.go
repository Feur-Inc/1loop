//go:build server

package main

import "encoding/json"

// Headless builds neither advertise nor start a desktop-control backend.
type browserPresentation struct{}
type computerContextKey struct{}

func computerToolDefinition() map[string]any     { return nil }
func computerInstructions(config) string         { return "" }
func computerNeedsApproval(json.RawMessage) bool { return true }

const terminalScreenVisibility = "On the headless server, captures work even without an attached or visible client. Text output is also readable in background chats."
