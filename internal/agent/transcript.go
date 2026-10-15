package agent

// A conversation's items as stored, and how a tool call is described,
// shared by the desktop and the headless server (the view is internal/ui/transcript.go).

import (
	"encoding/json"
	"strings"

	"1loop/internal/core"
)

type ChatItem struct {
	ID     string           `json:"id,omitempty"`    // stable prompt identity across edited versions
	Kind   string           `json:"kind"`            // user, assistant, error, note or tool
	Title  string           `json:"title,omitempty"` // tool rows saved by earlier versions
	Text   string           `json:"text"`            // the message, or a tool's output
	Images []core.ImagePart `json:"images,omitempty"`
	Tool   string           `json:"tool,omitempty"`   // tool rows: the tool's name
	Args   string           `json:"args,omitempty"`   // tool rows: the call's JSON arguments
	Diff   string           `json:"diff,omitempty"`   // edit_file rows: the diff, once shown for approval
	Millis int64            `json:"millis,omitempty"` // tool rows: execution time
	Failed string           `json:"failed,omitempty"` // tool rows: the error, when the call failed
}

func ToolArgs(raw string) (path, command string, creates bool) {
	var args struct {
		Path    string  `json:"path"`
		Command string  `json:"command"`
		Old     *string `json:"old_text"`
	}
	_ = json.Unmarshal([]byte(raw), &args)
	return args.Path, args.Command, args.Old != nil && *args.Old == ""
}

func DescribeTool(item ChatItem, running bool) (verb, target string, mono bool) {
	if item.Tool == "computer_use" {
		var request struct{ Action string }
		_ = json.Unmarshal([]byte(item.Args), &request)
		if running {
			return "Using desktop:", request.Action, false
		}
		return "Used desktop:", request.Action, false
	}
	if strings.HasPrefix(item.Tool, "terminal_") {
		if running {
			return "Using terminal:", strings.TrimPrefix(item.Tool, "terminal_"), false
		}
		return "Used terminal:", strings.TrimPrefix(item.Tool, "terminal_"), false
	}
	if strings.HasPrefix(item.Tool, "browser_") {
		if running {
			return "Using", strings.TrimPrefix(item.Tool, "browser_"), false
		}
		return "Used browser:", strings.TrimPrefix(item.Tool, "browser_"), false
	}
	path, command, creates := ToolArgs(item.Args)
	pick := func(done, doing string) string {
		if running {
			return doing
		}
		return done
	}
	switch item.Tool {
	case "shell":
		first, _, multi := strings.Cut(strings.TrimSpace(command), "\n")
		if multi {
			first += " …"
		}
		return pick("Ran", "Running"), first, true
	case "read_file":
		return pick("Read", "Reading"), path, false
	case "list_directory":
		if path == "" || path == "." {
			path = "workspace root"
		}
		return pick("Listed", "Listing"), path, false
	case "edit_file":
		if path == "" { // rows saved before arguments were kept
			path = diffPath(item.Diff)
		}
		if creates {
			return pick("Created", "Creating"), path, false
		}
		return pick("Edited", "Editing"), path, false
	}
	// A tool of another harness (Claude Code's Grep, WebFetch, Agent…):
	// its name and the argument that says what it is about.
	var args map[string]any
	_ = json.Unmarshal([]byte(item.Args), &args)
	for _, key := range []string{"pattern", "query", "url", "description", "skill", "subject", "prompt"} {
		if text, ok := args[key].(string); ok && text != "" {
			target = FirstLine(text)
			break
		}
	}
	switch item.Tool {
	case "Grep", "Glob":
		return pick("Searched", "Searching"), target, true
	case "WebSearch":
		return pick("Searched the web for", "Searching the web for"), target, false
	case "WebFetch":
		return pick("Fetched", "Fetching"), target, false
	case "Task", "Agent":
		return pick("Ran an agent:", "Running an agent:"), target, false
	case "TodoWrite", "update_todo", "mcp__1loop_todo__update_todo":
		return pick("Updated", "Updating"), "the task list", false
	case "recall":
		return pick("Recalled", "Recalling"), "conversation history", false
	case "checkpoint":
		return pick("Recorded", "Recording"), "a context checkpoint", false
	}
	if target == "" {
		return pick("Used", "Using"), item.Tool, false
	}
	return pick("Used", "Using") + " " + item.Tool + ":", target, false
}

func diffPath(diff string) string {
	for _, line := range strings.SplitN(diff, "\n", 3) {
		if strings.HasPrefix(line, "+++ b/") {
			return strings.TrimPrefix(line, "+++ b/")
		}
	}
	return "file"
}

func FirstLine(text string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return first
}
