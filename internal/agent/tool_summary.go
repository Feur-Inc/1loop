package agent

// What a tool with several actions did, from its call's arguments: "Took a
// browser screenshot" rather than "Used browser: read" (#76). The verb says
// whether it is done ("Took") or running ("Taking"), as for every row.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// action is one description: the done and running verbs, and the target.
type action struct {
	done, doing, target string
	mono                bool
}

// describeAction describes the tools whose name alone says too little; ok
// is false for the others.
func describeAction(item ChatItem, running bool) (verb, target string, mono, ok bool) {
	var described action
	switch {
	case strings.HasPrefix(item.Tool, "browser_"):
		described, ok = browserAction(item.Tool, item.Args)
	case strings.HasPrefix(item.Tool, "terminal_"):
		described, ok = terminalAction(item.Tool, item.Args)
	case item.Tool == "computer_use":
		described, ok = desktopAction(item.Args), true
	case item.Tool == "phone_use":
		described, ok = phoneAction(item.Args), true
	case strings.HasPrefix(item.Tool, "mcp__") && strings.Count(item.Tool, "__") >= 2:
		rest := strings.TrimPrefix(item.Tool, "mcp__")
		server, tool, _ := strings.Cut(rest, "__")
		described, ok = action{"Used " + server + ":", "Using " + server + ":", tool, false}, true
	default:
		described, ok = otherAction(item.Tool, item.Args)
	}
	if !ok {
		return "", "", false, false
	}
	verb = described.done
	if running {
		verb = described.doing
	}
	return verb, described.target, described.mono, true
}

// quoted is a short quotation of typed text.
func quoted(text string) string {
	text = FirstLine(text)
	if runes := []rune(text); len(runes) > 60 {
		text = string(runes[:59]) + "…"
	}
	return "“" + text + "”"
}

func point(x, y float64) string {
	return fmt.Sprintf("%.0f, %.0f", x, y)
}

func browserAction(tool, raw string) (action, bool) {
	var args BrowserToolArgs
	_ = json.Unmarshal([]byte(raw), &args)
	switch tool {
	case "browser_read":
		switch args.Action {
		case "screenshot":
			if args.FullPage {
				return action{"Took", "Taking", "a full-page browser screenshot", false}, true
			}
			return action{"Took", "Taking", "a browser screenshot", false}, true
		case "logs":
			return action{"Read", "Reading", "the browser logs", false}, true
		}
		return action{"Read", "Reading", "the page snapshot", false}, true
	case "browser_interact":
		where := args.Selector
		if where == "" {
			where = "at " + point(args.X, args.Y)
		}
		switch args.Action {
		case "click":
			return action{"Clicked", "Clicking", where, false}, true
		case "move":
			to := args.Selector
			if to == "" {
				to = point(args.X, args.Y)
			}
			return action{"Moved the pointer to", "Moving the pointer to", to, false}, true
		case "type":
			target := quoted(args.Text)
			if args.Selector != "" {
				target += " in " + args.Selector
			}
			return action{"Typed", "Typing", target, false}, true
		case "key":
			return action{"Pressed", "Pressing", args.Text, false}, true
		case "scroll":
			direction := "down"
			switch {
			case args.DeltaY < 0:
				direction = "up"
			case args.DeltaY == 0 && args.DeltaX > 0:
				direction = "right"
			case args.DeltaY == 0 && args.DeltaX < 0:
				direction = "left"
			}
			return action{"Scrolled the page", "Scrolling the page", direction, false}, true
		}
	case "browser_tabs":
		switch args.Action {
		case "list":
			return action{"Listed", "Listing", "the browser tabs", false}, true
		case "open":
			if args.URL != "" {
				return action{"Opened a browser tab:", "Opening a browser tab:", args.URL, false}, true
			}
			return action{"Opened", "Opening", "a browser tab", false}, true
		case "select":
			return action{"Switched", "Switching", "browser tab", false}, true
		case "close":
			return action{"Closed", "Closing", "a browser tab", false}, true
		}
	case "browser_navigate":
		switch args.Action {
		case "reload":
			return action{"Reloaded", "Reloading", "the page", false}, true
		case "back":
			return action{"Went back", "Going back", "a page", false}, true
		case "forward":
			return action{"Went forward", "Going forward", "a page", false}, true
		}
		if args.URL == "" {
			return action{"Opened", "Opening", "a page", false}, true
		}
		return action{"Opened", "Opening", args.URL, false}, true
	case "browser_evaluate":
		return action{"Ran JavaScript:", "Running JavaScript:", FirstLine(args.Expression), true}, true
	case "browser_resize":
		if args.Mode == "adaptive" {
			return action{"Fitted the browser", "Fitting the browser", "to the panel", false}, true
		}
		size := fmt.Sprintf("%d × %d", args.Width, args.Height)
		if args.Mobile {
			size += " (mobile)"
		}
		return action{"Resized the browser to", "Resizing the browser to", size, false}, true
	}
	name := strings.TrimPrefix(tool, "browser_")
	return action{"Used browser:", "Using browser:", name, false}, true
}

func terminalAction(tool, raw string) (action, bool) {
	var args struct {
		Action  string
		Cwd     string
		Command string
		Text    string
		Keys    []string
	}
	_ = json.Unmarshal([]byte(raw), &args)
	switch tool {
	case "terminal_tabs":
		switch args.Action {
		case "list":
			return action{"Listed", "Listing", "the terminals", false}, true
		case "open":
			if args.Cwd != "" {
				return action{"Opened a terminal in", "Opening a terminal in", args.Cwd, false}, true
			}
			return action{"Opened", "Opening", "a terminal", false}, true
		case "select":
			return action{"Switched", "Switching", "terminal", false}, true
		case "close":
			return action{"Closed", "Closing", "a terminal", false}, true
		}
	case "terminal_command":
		command := FirstLine(args.Command)
		if strings.Contains(strings.TrimSpace(args.Command), "\n") {
			command += " …"
		}
		return action{"Ran in the terminal:", "Running in the terminal:", command, true}, true
	case "terminal_write":
		return action{"Typed in the terminal:", "Typing in the terminal:", quoted(args.Text), false}, true
	case "terminal_keys":
		return action{"Pressed in the terminal:", "Pressing in the terminal:", strings.Join(args.Keys, ", "), false}, true
	case "terminal_read":
		return action{"Read", "Reading", "the terminal output", false}, true
	case "terminal_screen":
		return action{"Took", "Taking", "a terminal screenshot", false}, true
	}
	name := strings.TrimPrefix(tool, "terminal_")
	return action{"Used terminal:", "Using terminal:", name, false}, true
}

// desktopAction reads computer_use's action and the JSON object of its
// arguments.
func desktopAction(raw string) action {
	var call struct {
		Action    string
		Arguments string
	}
	_ = json.Unmarshal([]byte(raw), &call)
	var args struct {
		X, Y                  *float64
		Text, Key, Title, App string
		Keys                  []string
	}
	_ = json.Unmarshal([]byte(call.Arguments), &args)
	at := "on the desktop"
	if args.X != nil && args.Y != nil {
		at = "at " + point(*args.X, *args.Y)
	}
	switch call.Action {
	case "instructions":
		return action{"Read", "Reading", "Computer Use's instructions", false}
	case "start":
		return action{"Started", "Starting", "Computer Use", false}
	case "stop":
		return action{"Stopped", "Stopping", "Computer Use", false}
	case "screenshot":
		return action{"Took", "Taking", "a desktop screenshot", false}
	case "get_app_state", "describe":
		return action{"Read", "Reading", "the app's accessibility tree", false}
	case "doctor":
		return action{"Checked", "Checking", "Computer Use's setup", false}
	case "list_windows":
		return action{"Listed", "Listing", "the windows", false}
	case "focused_window":
		return action{"Read", "Reading", "the focused window", false}
	case "activate_window":
		target := args.Title
		if target == "" {
			target = args.App
		}
		if target == "" {
			target = "a window"
		}
		return action{"Switched to", "Switching to", target, false}
	case "click":
		return action{"Clicked", "Clicking", at, false}
	case "move":
		to := "on the desktop"
		if args.X != nil && args.Y != nil {
			to = "to " + point(*args.X, *args.Y)
		}
		return action{"Moved the pointer", "Moving the pointer", to, false}
	case "scroll":
		return action{"Scrolled", "Scrolling", at, false}
	case "drag":
		return action{"Dragged", "Dragging", at, false}
	case "press_key":
		keys := args.Key
		if keys == "" {
			keys = strings.Join(args.Keys, ", ")
		}
		return action{"Pressed", "Pressing", keys, false}
	case "type_text":
		return action{"Typed", "Typing", quoted(args.Text), false}
	case "perform_action":
		return action{"Performed", "Performing", "an accessibility action", false}
	case "set_value":
		return action{"Set", "Setting", "a field's value", false}
	case "move_window":
		return action{"Moved", "Moving", "a window", false}
	case "resize_window":
		return action{"Resized", "Resizing", "a window", false}
	case "setup_accessibility":
		return action{"Set up", "Setting up", "accessibility access", false}
	}
	return action{"Used desktop:", "Using desktop:", call.Action, false}
}

func phoneAction(raw string) action {
	var args struct {
		Action string
		X, Y   float64
		Text   string
	}
	_ = json.Unmarshal([]byte(raw), &args)
	switch args.Action {
	case "state":
		return action{"Read", "Reading", "the phone's screen", false}
	case "screenshot":
		return action{"Took", "Taking", "a phone screenshot", false}
	case "tap":
		return action{"Tapped the phone", "Tapping the phone", "at " + point(args.X, args.Y), false}
	case "swipe":
		return action{"Swiped", "Swiping", "on the phone", false}
	case "type":
		return action{"Typed on the phone:", "Typing on the phone:", quoted(args.Text), false}
	case "key":
		return action{"Pressed on the phone:", "Pressing on the phone:", args.Text, false}
	}
	return action{"Used the phone:", "Using the phone:", args.Action, false}
}

// otherAction covers 1loop's other tools with an action or a subject.
func otherAction(tool, raw string) (action, bool) {
	var args struct {
		Action    string
		ID        *string
		Name      string
		File      *string
		Group     string
		Command   string
		Packages  []string
		Folders   []string
		Network   bool
		Questions []struct{ Header, Question string }
		// github_upload_attachments, github_view_attachment
		Repository string
		Files      []json.RawMessage
		URL        string
	}
	_ = json.Unmarshal([]byte(raw), &args)
	switch tool {
	case "background_task":
		id := "a background task"
		if args.ID != nil && *args.ID != "" {
			id = "background task " + *args.ID
		}
		switch args.Action {
		case "list":
			return action{"Listed", "Listing", "the background tasks", false}, true
		case "stop":
			return action{"Stopped", "Stopping", id, false}, true
		}
		return action{"Read the output of", "Reading the output of", id, false}, true
	case "skill":
		target := args.Name
		if args.File != nil && *args.File != "" {
			target += " · " + *args.File
		}
		return action{"Loaded the skill", "Loading the skill", target, false}, true
	case "tool_search":
		names := map[string]string{"panel_browser": "browser", "panel_terminal": "terminal", "local_phone": "phone", "local_debian": "Debian package", "desktop_computer": "Computer Use"}
		group := names[args.Group]
		if group == "" {
			group = strings.ReplaceAll(args.Group, "_", " ")
		}
		group = "the " + group + " tools"
		if server, ok := strings.CutPrefix(args.Group, "mcp_"); ok {
			group = "the " + server + " MCP tools"
		}
		return action{"Loaded", "Loading", group, false}, true
	case githubUploadTool:
		files := "1 file"
		if len(args.Files) != 1 {
			files = fmt.Sprintf("%d files", len(args.Files))
		}
		return action{"Uploaded", "Uploading", files + " to GitHub · " + args.Repository, false}, true
	case githubViewTool:
		return action{"Opened the GitHub attachment", "Opening the GitHub attachment", args.URL, false}, true
	case "adb_shell":
		return action{"Ran on the phone:", "Running on the phone:", FirstLine(args.Command), true}, true
	case "install_packages":
		return action{"Installed", "Installing", strings.Join(args.Packages, ", "), false}, true
	case "request_user_input":
		if len(args.Questions) == 1 {
			return action{"Asked:", "Asking:", FirstLine(args.Questions[0].Question), false}, true
		}
		return action{"Asked", "Asking", fmt.Sprintf("%d questions", len(args.Questions)), false}, true
	case "request_permissions":
		var parts []string
		if len(args.Folders) > 0 {
			parts = append(parts, strings.Join(args.Folders, ", "))
		}
		if args.Network {
			parts = append(parts, "network access")
		}
		if len(parts) == 0 {
			parts = append(parts, "access")
		}
		return action{"Asked for", "Asking for", strings.Join(parts, " and "), false}, true
	}
	return action{}, false
}

// CallLabel says what the model is writing a call for, before its
// arguments are complete: detail is the path or command seen so far.
func CallLabel(tool, detail string) string {
	detail = FirstLine(detail)
	label := ""
	switch tool {
	case "shell":
		label = "Writing a command"
	case "edit_file":
		label = "Preparing an edit"
		if detail != "" {
			label, detail = label+" to "+detail, ""
		}
	case "read_file":
		label = "Choosing a file to read"
		if detail != "" {
			label, detail = "Preparing to read "+detail, ""
		}
	case "list_directory":
		label = "Preparing to list a directory"
	case "web_search":
		label = "Searching the web"
	case "tool_search":
		label = "Loading tools"
	default:
		label = "Preparing " + toolNoun(tool)
	}
	if detail != "" {
		label += ": " + detail
	}
	return label
}

// toolNoun names a tool for people.
func toolNoun(tool string) string {
	switch {
	case strings.HasPrefix(tool, "browser_"):
		return "a browser action"
	case strings.HasPrefix(tool, "terminal_"):
		return "a terminal action"
	case strings.HasPrefix(tool, "mcp__") && strings.Count(tool, "__") >= 2:
		server, name, _ := strings.Cut(strings.TrimPrefix(tool, "mcp__"), "__")
		return server + " · " + name
	}
	switch tool {
	case "computer_use":
		return "a desktop action"
	case "phone_use":
		return "a phone action"
	case "adb_shell":
		return "a phone command"
	case "skill":
		return "a skill"
	case "background_task":
		return "a background task"
	case "update_todo":
		return "the task list"
	case "request_user_input":
		return "a question"
	case "request_permissions":
		return "a permission request"
	case "install_packages":
		return "a package install"
	case githubUploadTool:
		return "a GitHub upload"
	case githubViewTool:
		return "a GitHub attachment"
	}
	return tool
}
