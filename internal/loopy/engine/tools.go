// SPDX-License-Identifier: MPL-2.0

//go:build !js

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"1loop/internal/loopy"
	"1loop/internal/loopy/memory"
)

// allowed is every tool a run may use: Loopy's own, the browser's, and
// compaction's checkpoint. Never a shell, the user's files or screen.
func allowed(name string) bool {
	return strings.HasPrefix(name, "loopy_") || strings.HasPrefix(name, "browser_") || name == "checkpoint" || name == "tool_search"
}

func define(name, description string, properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description,
		"parameters": map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}}
}

func text(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func one(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}

func list(description string, of map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": of, "description": description}
}

func object(properties map[string]any) map[string]any {
	var required []string
	for name := range properties {
		required = append(required, name)
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func tools(e *Engine) []map[string]any {
	var reactions []string
	for _, reaction := range loopy.Reactions {
		reactions = append(reactions, reaction.Name)
	}
	plain := map[string]any{"type": "string"}
	out := []map[string]any{
		define("loopy_react", "React to the user's latest message instead of answering words such as \"ok\".",
			map[string]any{"reaction": one("", reactions...)}, "reaction"),
		define("loopy_remember", "Keep something about the user for good, in one short sentence complete on its own, in their language (\"Préfère les trains avant 10 h\"). kind \"instruction\": a rule for you; \"style\": how to answer them or write for them. When it changes or corrects a fact you were given, name that fact in replaces. Never a password, code, card or ID number.",
			map[string]any{"fact": text("One sentence."), "kind": one("", memory.Kinds...), "pinned": map[string]any{"type": "boolean", "description": "It matters in most conversations."}, "replaces": text("The id of the fact it replaces, or empty.")}, "fact", "kind", "pinned", "replaces"),
		define("loopy_forget", "Forget facts for good: by id, or those whose text holds these words. exchanges also removes the conversation they come from.",
			map[string]any{"id": text("A fact's id, or empty."), "words": text("Or words of the facts."), "exchanges": map[string]any{"type": "boolean"}}, "id", "words", "exchanges"),
		define("loopy_recall", "Search everything you keep of the user: the facts (those that ended included), what was said, what you did. Give several wordings (other words, the other language); a period when the question is about one. Search again with what you found if something is missing.",
			map[string]any{"queries": list("One to four wordings.", plain), "since": text("YYYY-MM-DD, or empty."), "until": text("YYYY-MM-DD, or empty.")}, "queries", "since", "until"),
		define("loopy_card", "Show a card for the user to act on; the answer comes later as a message.", map[string]any{
			"kind":     one("", "mail", "message", "event", "choice", "needs_you", "summary"),
			"title":    text("Short."),
			"subtitle": text("A detail beside it, or empty."),
			"fields":   list("Labelled values: To, Cc, Subject; What, When, Where.", object(map[string]any{"label": plain, "value": plain})),
			"body":     text("The text: the draft, what is asked. Empty if none."),
			"options":  list("The options of a choice; a summary's lines, each with its action (\"See\", \"Answer\").", object(map[string]any{"label": plain, "detail": plain, "aside": text("A price or a time, or empty."), "action": text("A summary line's button, or empty.")})),
			"warning":  text("What to be careful of, or empty."),
			"ask":      text("needs_you: what they must type (\"The code received by SMS\"), or empty."),
			"actions":  list("Buttons, the main one first. Empty: the kind's own (Send, Discard…).", plain),
		}, "kind", "title", "subtitle", "fields", "body", "options", "warning", "ask", "actions"),
		define("loopy_files", "Your own folder. list, read (from line), write, append, edit (old text, replaced by text), move (to), remove (a file or an empty folder), search (a pattern in names and text).", map[string]any{
			"action": one("", "list", "read", "write", "append", "edit", "move", "remove", "search"),
			"path":   text("The file or folder, as /notes/x.md; / for all."), "text": text("What to write; edit: the new text; search: the pattern. Empty otherwise."),
			"old": text("edit: the text to replace; empty otherwise."), "to": text("move: where; empty otherwise."), "line": map[string]any{"type": "integer", "description": "read: the first line, 1 for all."}}, "action", "path", "text", "old", "to", "line"),
		define("loopy_run", "Run JavaScript in your small computer (QuickJS: std, os; no network, no await at the top). Returns what it prints.",
			map[string]any{"script": text("The script; or empty with file."), "file": text("A script kept in your folder, or empty."), "seconds": map[string]any{"type": "integer", "description": "At most; 30 by default, up to 300."}}, "script", "file", "seconds"),
		define("loopy_delegate", "Propose a coding task for a coding agent in one of the user's projects. The user starts it from the card; you follow it.",
			map[string]any{"project": text("As listed."), "title": text("Short."), "brief": text("Everything the coding agent needs: what to do, where, how to check it, whether to open a pull request.")}, "project", "title", "brief"),
		define("loopy_routine", "Propose a routine, or change one of yours (id). It runs on a schedule (when) or when something happens (trigger, filter).", map[string]any{
			"id": text("The routine to change, or empty."), "title": text("Short."), "task": text("What to do each time, as a message to yourself, complete on its own."),
			"when":    text("\"daily 08:00\", \"weekdays 09:30\", \"mon,thu 18:00\", \"monthly 1 09:00\", \"once 2026-12-24 18:00\", \"every 2h\"; empty with a trigger."),
			"trigger": one("", "", "github", "mail", "webhook"), "filter": text("github: \"owner/name issue\", \"… pr\" or \"… ci\"; mail: words of the sender or subject, or empty; else empty."),
			"watch": map[string]any{"type": "boolean", "description": "It keeps an eye on something, at night too, and wakes the user only if it breaks."}}, "id", "title", "task", "when", "trigger", "filter", "watch"),
		define("loopy_sign_in", "Have the user signed in on the page open in your browser. They type in a form of 1loop's, or allow their vault's entry; it goes straight into the page's fields: you never see it. Then press the page's button yourself.",
			map[string]any{"why": text("What for, in a few words."), "user_field": text("CSS selector of the user name's field, or empty."), "password_field": text("CSS selector of the password's field, or empty when the page asks for it next."), "entry": text("The vault entry's name to use when there are several, or empty.")}, "why", "user_field", "password_field", "entry"),
		define("loopy_quiet", "This run of your own has nothing to tell the user: it ends without a word.", map[string]any{"note": text("What you checked, for your journal.")}, "note"),
		define("loopy_notify", "Wake the user with a notification at once, even at night. Only when something they asked you to watch breaks or cannot wait.", map[string]any{"text": text("One sentence.")}, "text"),
	}
	mail, calendar := e.accounts()
	if mail {
		out = append(out, define("loopy_mail", "Read the user's mail (never sends: for that, a mail card). list: the latest, or those matching words; read: one, by id.", map[string]any{
			"action": one("", "list", "read"), "words": text("list: words of the sender, subject or text, or empty."), "unread": map[string]any{"type": "boolean"}, "since": text("YYYY-MM-DD, or empty."), "id": text("read: the mail's id.")}, "action", "words", "unread", "since", "id"))
	}
	if calendar {
		out = append(out, define("loopy_calendar", "Read the user's calendar between two days. To add something, an event card.", map[string]any{"from": text("YYYY-MM-DD."), "to": text("YYYY-MM-DD, included.")}, "from", "to"))
	}
	return out
}

type toolArgs struct {
	Reaction, Fact, Kind, Replaces, ID, Words, Since, Until          string
	Title, Subtitle, Body, Warning, Ask, Action, Path, Text, Old, To string
	Script, File, Project, Brief, When, Trigger, Filter, Task        string
	Why, Entry, Note, From                                           string
	UserField                                                        string `json:"user_field"`
	PasswordField                                                    string `json:"password_field"`
	Pinned, Exchanges, Watch, Unread                                 bool
	Line, Seconds                                                    int
	Queries, Actions                                                 []string
	Fields                                                           []loopy.Field
	Options                                                          []loopy.Option
}

// tool runs one of Loopy's tools.
func (e *Engine) tool(ctx context.Context, name string, arguments json.RawMessage) (string, error) {
	var args toolArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return "", fmt.Errorf("these arguments cannot be read: %w", err)
	}
	e.mu.Lock()
	r := e.run
	e.mu.Unlock()
	if r == nil || ctx.Err() != nil {
		return "", context.Canceled
	}
	state, label := toolActivity(name, "")
	e.set(state, label)
	switch name {
	case "loopy_react":
		e.store.Lock()
		id := ""
		for _, m := range slices.Backward(e.store.Messages) {
			if m.From == "me" {
				id = m.ID
				break
			}
		}
		e.store.Unlock()
		if _, ok := e.update(id, func(m *loopy.Message) { m.Reaction = args.Reaction }); !ok {
			return "", errors.New("the user has written nothing yet")
		}
		return "Reacted. If nothing else is needed, end your turn without words.", nil
	case "loopy_remember":
		return e.remember(r, args)
	case "loopy_forget":
		return e.forget(args)
	case "loopy_recall":
		return e.recall(args)
	case "loopy_card":
		return e.card(r, args)
	case "loopy_files":
		return e.files(args)
	case "loopy_run":
		return e.script(ctx, args)
	case "loopy_delegate":
		return e.proposeTask(r, args.Project, args.Title, args.Brief)
	case "loopy_routine":
		return e.proposeRoutine(r, args)
	case "loopy_sign_in":
		return e.signIn(ctx, args)
	case "loopy_quiet":
		r.quiet = true
		e.log(args.Note)
		return "Good. End your turn without words.", nil
	case "loopy_notify":
		e.notify("Loopy", args.Text, nil, true)
		r.said = true
		return "Sent.", nil
	case "loopy_mail":
		return e.mailTool(ctx, args)
	case "loopy_calendar":
		return e.calendarTool(ctx, args)
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

func (e *Engine) remember(r *run, args toolArgs) (string, error) {
	if masked, found := sensitive(args.Fact, e.secrets()); len(found) > 0 {
		return "", fmt.Errorf("that holds %s, which memory never keeps: %q", strings.Join(found, " and "), masked)
	}
	fact, err := e.memory.Remember(memory.Fact{Text: args.Fact, Kind: args.Kind, Pinned: args.Pinned, Said: r.why == "", From: r.record}, args.Replaces)
	if err != nil {
		return "", err
	}
	e.add(loopy.Message{From: "loopy", Memory: "I'll remember: " + fact.Text, MemoryID: fact.ID})
	e.tell(nil, nil, true)
	return "Kept (id " + fact.ID + "). The user sees it, with a Forget button: do not repeat it.", nil
}

func (e *Engine) forget(args toolArgs) (string, error) {
	if args.ID == "" && len(strings.TrimSpace(args.Words)) < 3 {
		return "", errors.New("name a fact's id, or words of it")
	}
	gone, err := e.memory.Forget(args.ID, args.Words, args.Exchanges)
	if err != nil {
		return "", err
	}
	if len(gone) == 0 {
		return "No fact holds that.", nil
	}
	var texts []string
	for _, fact := range gone {
		texts = append(texts, fact.Text)
	}
	e.add(loopy.Message{From: "loopy", Memory: "I forgot: " + strings.Join(texts, "; ")})
	e.tell(nil, nil, true)
	return fmt.Sprintf("Forgot %d.", len(gone)), nil
}

func (e *Engine) recall(args toolArgs) (string, error) {
	var queries []string
	for _, query := range args.Queries {
		if strings.TrimSpace(query) != "" && len(queries) < 4 {
			queries = append(queries, query)
		}
	}
	if len(queries) == 0 {
		return "", errors.New("give words to look for")
	}
	day := func(s string, after int) time.Time {
		t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local)
		if err != nil {
			return time.Time{}
		}
		return t.AddDate(0, 0, after)
	}
	return e.memory.FormatSearch(queries, day(args.Since, 0), day(args.Until, 1), 14), nil
}

func (e *Engine) card(r *run, args toolArgs) (string, error) {
	card := loopy.Card{Kind: args.Kind, Title: strings.TrimSpace(args.Title), Subtitle: args.Subtitle, Fields: args.Fields, Body: args.Body,
		Options: args.Options, Warning: args.Warning, Actions: args.Actions}
	if card.Title == "" {
		return "", errors.New("give the card a title")
	}
	for i := range card.Actions { // 1loop's own words only for its own cards
		if len(card.Actions[i]) > 40 {
			return "", errors.New("a button is a word or two")
		}
	}
	switch args.Kind {
	case "mail", "message", "event":
		card.Editable = true
		card.Actions = map[string][]string{"mail": {"Send", "Discard"}, "message": {"Send", "Discard"}, "event": {"Add", "Discard"}}[args.Kind]
		if args.Kind == "event" {
			if _, err := parseEvent(card.Fields, card.Body); err != nil {
				return "", err
			}
		}
		if args.Kind == "mail" {
			if problem := checkMail(card.Fields); problem != nil {
				return "", problem
			}
		}
	case "choice":
		if len(card.Options) < 2 {
			return "", errors.New("a choice has at least two options")
		}
		card.Actions = nil
	case "needs_you":
		card.Ask = args.Ask
		if low := plain(args.Ask); strings.Contains(low, "password") || strings.Contains(low, "mot de passe") || strings.Contains(low, "card number") || strings.Contains(low, "numero de carte") {
			return "", errors.New("never ask for a password or a card: use loopy_sign_in for a sign-in")
		}
		if len(card.Actions) == 0 && card.Ask == "" {
			card.Actions = []string{imDone, "Drop it"}
		}
		if card.Ask != "" {
			card.Actions = []string{"Give it", "Drop it"}
		}
	case "summary":
		card.Actions = nil
	default:
		return "", errors.New("unknown kind of card")
	}
	r.said = true
	message := e.add(loopy.Message{From: "loopy", Card: &card})
	if !card.Waiting() {
		return "Shown.", nil
	}
	e.notify(card.Title, card.Body, &message, args.Kind == "needs_you")
	return "Shown. The answer comes as a later message: end your turn now, without repeating it.", nil
}

func (e *Engine) files(args toolArgs) (string, error) {
	switch args.Action {
	case "list":
		files, err := e.folder.list(args.Path, true)
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			return "Empty.", nil
		}
		var b strings.Builder
		for _, file := range files {
			if file.Dir {
				fmt.Fprintf(&b, "%s/\n", file.Path)
			} else {
				fmt.Fprintf(&b, "%s  %d bytes  %s\n", file.Path, file.Size, file.Time.Local().Format("2006-01-02 15:04"))
			}
		}
		return b.String(), nil
	case "read":
		content, err := e.folder.read(args.Path, args.Line)
		if err != nil {
			return "", err
		}
		if name, _ := clean(args.Path); strings.HasPrefix(name, "inbox/") {
			content = untrusted("file", content)
		}
		return redact(content, e.secrets()), nil
	case "write", "append":
		return "Written.", e.folder.write(args.Path, []byte(args.Text), args.Action == "append")
	case "edit":
		return "Changed.", e.folder.edit(args.Path, args.Old, args.Text)
	case "move":
		return "Moved.", e.folder.move(args.Path, args.To)
	case "remove":
		return "Removed.", e.folder.remove(args.Path)
	case "search":
		return e.folder.search(args.Text)
	}
	return "", errors.New("unknown action")
}

func (e *Engine) script(ctx context.Context, args toolArgs) (string, error) {
	script := args.Script
	if args.File != "" {
		data, err := e.folder.bytes(args.File, 1<<20)
		if err != nil {
			return "", err
		}
		script = string(data)
	}
	if strings.TrimSpace(script) == "" {
		return "", errors.New("no script")
	}
	result, err := e.box.Run(ctx, e.folder.dir, script, time.Duration(args.Seconds)*time.Second)
	if err != nil {
		return "", err
	}
	if e.folder.size() > maxFolder {
		result.Output += "\n(your folder is over its limit of 2 GB: remove files)"
	}
	out := strings.TrimRight(result.Output, "\n")
	if out == "" {
		out = "(it printed nothing)"
	}
	if result.Failed {
		return "", errors.New(redact(out, e.secrets()))
	}
	return redact(out, e.secrets()), nil
}

func (e *Engine) secrets() []string {
	if e.vault == nil {
		return nil
	}
	return e.vault.secrets()
}
