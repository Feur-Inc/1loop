// SPDX-License-Identifier: MPL-2.0

//go:build !js

// Package engine makes Loopy work, wherever it lives: in the desktop app,
// on a 1loop server, or in the phone's own engine (a 1loop server too).
// What it needs of that place is Host; its views reach it through
// loopy.Client (the desktop directly, the others over HTTP).
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"1loop/internal/agent"
	"1loop/internal/browser"
	"1loop/internal/loopy"
	"1loop/internal/loopy/box"
	"1loop/internal/loopy/memory"
	"1loop/internal/loopy/python"
)

// Host is where Loopy lives.
type Host interface {
	Name() string
	Backend() *agent.App
	// Config is a run's configuration with Loopy's model (the host's own
	// when settings name none), in workspace, its browser included.
	Config(settings loopy.Settings, workspace string) (agent.Config, error)
	Models() []loopy.Model
	// Page is the browser page Loopy works in, nil without one. Its browser
	// is its own: another process and profile than Browser Use's.
	Page() *browser.Page
	// Pages are all that are open.
	Pages() []*browser.Page
	// OpenPage opens one for a script, which records its requests; ClosePage
	// closes it, ClosePages all of them, and the browser with them.
	OpenPage(info browser.Info) (*browser.Page, error)
	ClosePage(id string)
	ClosePages()
	Projects() []string
	// StartChat starts a project chat on a brief; FollowChat tells of it.
	StartChat(project, title, brief string) (string, error)
	StopChat(id string)
	// Chats are 1loop's; DeleteChat deletes one, with its subchats.
	Chats() []loopy.Chat
	DeleteChat(id string) error
	// Notify tells the user, when they are not looking at Loopy (whatever
	// they look at, for an urgent one).
	Notify(notice loopy.Notice)
	// GitHub is what to authorize a request to GitHub with, "" without the
	// connector.
	GitHub(ctx context.Context) string
	// Changed tells the views.
	Changed(change loopy.Change)
}

// Engine is a Loopy.
type Engine struct {
	host   Host
	dir    string
	store  *loopy.Store
	memory *memory.Memory
	folder *folder
	box    *box.Box
	vault  *vault // the accounts' passwords, and what came before the safe
	safe   *safe  // the user's vault, behind their master password
	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	version    int64
	run        *run
	queue      []queued // own runs waiting: routines, answers
	state      string
	label      string
	calling    string // the tool behind the state, "" for none
	used       int
	window     int
	waits      map[string]chan loopy.Answer // a run waiting on a card
	hands      chan struct{}                // the user has the browser; closed when they give it back
	filled     string                       // the site a secret was put in a page of
	granted    map[string]time.Time         // what a card just allowed, once
	learning   bool
	learnAfter time.Time
	settle     *time.Timer
	saveTimer  *time.Timer
	problem    string
	closed     bool
	wait       sync.WaitGroup
	github     map[string]string // etags
	browsed    time.Time         // its browser was last used
	links      links
	mails      map[string]uint32
	now        func() time.Time
}

// run is a run in progress.
type run struct {
	cancel  context.CancelFunc
	why     string // "" for the user's; else "routine …", "summary"
	routine string
	record  string // the memory record of the exchange
	stream  string // the message being written
	steer   []string
	said    bool   // it wrote something or showed a card
	quiet   bool   // it has nothing to say (loopy_quiet)
	link    string // the AutoLink that woke it
	stopped bool
	answer  strings.Builder
}

type queued struct {
	text    string // for the model
	message string // the user's message, when it is one
	why     string
	routine string
	link    string
}

// Open wakes the Loopy kept in dir.
func Open(ctx context.Context, dir string, host Host) (*Engine, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	store, problem := loopy.Open(filepath.Join(dir, "loopy.json"))
	if problem != nil {
		// Put aside, never overwritten.
		aside := filepath.Join(dir, "loopy.json.unreadable-"+time.Now().Format("20060102-150405"))
		if err := os.Rename(filepath.Join(dir, "loopy.json"), aside); err != nil {
			return nil, problem
		}
		if store, problem = loopy.Open(filepath.Join(dir, "loopy.json")); problem != nil {
			return nil, problem
		}
	}
	mem, err := memory.Open(filepath.Join(dir, "memory"))
	if err != nil {
		return nil, err
	}
	home, err := openFolder(filepath.Join(dir, "home"))
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"notes", "inbox", "work"} {
		home.root.MkdirAll(name, 0700)
	}
	e := &Engine{host: host, dir: dir, store: store, memory: mem, folder: home, box: box.New(filepath.Join(dir, "box")),
		state: "idle", waits: map[string]chan loopy.Answer{}, granted: map[string]time.Time{}, github: map[string]string{}, mails: map[string]uint32{}, now: time.Now}
	e.links = links{python: python.New(filepath.Join(dir, "python")), running: map[string]context.CancelFunc{}, slots: make(chan struct{}, 3), waiting: map[string]linkWait{}, did: map[string][]time.Time{}, pages: map[string]int{}}
	if e.vault, err = openVault(dir); err != nil {
		e.problem = err.Error() // the rest of Loopy works without it
	}
	if e.safe, err = openSafe(dir); err != nil {
		e.problem = err.Error()
	}
	e.ctx, e.cancel = context.WithCancel(ctx)
	store.Lock()
	for _, message := range store.Messages {
		e.version = max(e.version, message.Version)
	}
	for i := range store.Messages { // a run cut by the end of 1loop
		m := &store.Messages[i]
		m.Pending = false
		if m.Card.Waiting() && (m.Card.Kind == "approval" || m.Card.Kind == "unlock" || m.Card.Kind == "chats" || m.Card.Kind == "signin" || m.Card.Kind == "secret") && m.Card.Task == "" {
			m.Card.Answer, m.Card.Result = "Expired", "1loop closed before your answer. Ask again."
		}
	}
	for i := range store.Tasks {
		if store.Tasks[i].Status == "running" || store.Tasks[i].Status == "waiting" {
			store.Tasks[i].Status = "cancelled"
			store.Tasks[i].Steps = append(store.Tasks[i].Steps, loopy.Step{Time: e.now(), Text: "1loop closed while it worked"})
		}
	}
	old := store.State.Memory
	store.State.Memory = nil
	store.Unlock()
	for _, fact := range old {
		mem.Remember(memory.Fact{Text: fact.Text, Kind: fact.Category, Pinned: fact.Pinned, Since: fact.Time, Said: true}, "")
	}
	e.cleanTemp()
	e.wait.Add(1)
	go func() { defer e.wait.Done(); e.clockLoop() }()
	return e, nil
}

// Close ends what runs and keeps everything.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	if e.settle != nil {
		e.settle.Stop()
	}
	e.mu.Unlock()
	e.cancel()
	e.wait.Wait()
	e.memory.Save()
	e.store.Save()
	e.folder.root.Close()
}

// Busy says whether a run, or a thing to run soon, keeps Loopy awake.
func (e *Engine) Busy() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.run != nil || len(e.queue) > 0 || e.learning
}

// Wakes says whether Loopy must stay awake to do what it was given: a
// routine that is on, or a coding task.
func (e *Engine) Wakes() bool {
	e.store.Lock()
	defer e.store.Unlock()
	for _, routine := range e.store.Routines {
		if routine.On {
			return true
		}
	}
	for _, link := range e.store.Links {
		if link.On {
			return true
		}
	}
	return slices.ContainsFunc(e.store.Tasks, func(task loopy.Task) bool { return task.Status == "running" || task.Status == "waiting" })
}

// save writes the state soon.
func (e *Engine) save() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	if e.saveTimer == nil {
		e.saveTimer = time.AfterFunc(400*time.Millisecond, func() {
			e.mu.Lock()
			e.saveTimer = nil
			e.mu.Unlock()
			if err := e.store.Save(); err != nil {
				e.fail("Loopy's conversation could not be saved: " + err.Error())
			}
		})
	}
}

func (e *Engine) fail(text string) {
	e.mu.Lock()
	e.problem = text
	e.mu.Unlock()
	e.tell(nil, nil, true)
}

// ---------------------------------------------------------------------------
// Messages and changes.

// add appends a message.
func (e *Engine) add(message loopy.Message) loopy.Message {
	if message.ID == "" {
		message.ID = loopy.NewID()
	}
	if message.Time.IsZero() {
		message.Time = e.now()
	}
	e.mu.Lock()
	e.version++
	message.Version = e.version
	e.mu.Unlock()
	e.store.Lock()
	e.store.Messages = append(e.store.Messages, message)
	e.store.Unlock()
	e.tell([]loopy.Message{message}, nil, message.Card != nil)
	e.save()
	return message
}

// update changes a message.
func (e *Engine) update(id string, change func(*loopy.Message)) (loopy.Message, bool) {
	e.mu.Lock()
	e.version++
	version := e.version
	e.mu.Unlock()
	e.store.Lock()
	for i := len(e.store.Messages) - 1; i >= 0; i-- {
		if e.store.Messages[i].ID == id {
			change(&e.store.Messages[i])
			e.store.Messages[i].Version = version
			message := copyMessage(e.store.Messages[i])
			e.store.Unlock()
			e.tell([]loopy.Message{message}, nil, message.Card != nil)
			e.save()
			return message, true
		}
	}
	e.store.Unlock()
	return loopy.Message{}, false
}

func copyMessage(message loopy.Message) loopy.Message {
	if message.Card != nil {
		card := *message.Card
		card.Fields, card.Options, card.Actions = slices.Clone(card.Fields), slices.Clone(card.Options), slices.Clone(card.Actions)
		message.Card = &card
	}
	message.Files = slices.Clone(message.Files)
	return message
}

func (e *Engine) message(id string) (loopy.Message, bool) {
	e.store.Lock()
	defer e.store.Unlock()
	for i := len(e.store.Messages) - 1; i >= 0; i-- {
		if e.store.Messages[i].ID == id {
			return copyMessage(e.store.Messages[i]), true
		}
	}
	return loopy.Message{}, false
}

func (e *Engine) drop(id string) {
	e.store.Lock()
	e.store.Messages = slices.DeleteFunc(e.store.Messages, func(m loopy.Message) bool { return m.ID == id })
	e.store.Unlock()
	e.tell(nil, []string{id}, false)
	e.save()
}

// tell hands the views what changed.
func (e *Engine) tell(messages []loopy.Message, gone []string, all bool) {
	e.mu.Lock()
	change := loopy.Change{Version: e.version, Messages: messages, Gone: gone, State: e.state, Label: e.label, Tool: e.calling, Running: e.run != nil, Used: e.used, Window: e.window, All: all}
	e.mu.Unlock()
	e.host.Changed(change)
}

// set is the face's state and its words.
func (e *Engine) set(state, label string) { e.setCalling(state, label, "") }

// setCalling changes the face for what a tool does.
func (e *Engine) setCalling(state, label, tool string) {
	e.mu.Lock()
	if e.settle != nil {
		e.settle.Stop()
		e.settle = nil
	}
	same := e.state == state && e.label == label && e.calling == tool
	e.state, e.label, e.calling = state, label, tool
	e.mu.Unlock()
	if !same {
		e.tell(nil, nil, false)
	}
}

// rest brings the face back to rest after a while.
func (e *Engine) rest(after time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	if e.settle != nil {
		e.settle.Stop()
	}
	e.settle = time.AfterFunc(after, func() {
		e.mu.Lock()
		running := e.run != nil
		e.mu.Unlock()
		if running {
			return
		}
		if len(e.waiting()) > 0 {
			e.set("waiting", "Waiting for you")
		} else {
			e.set("idle", "")
		}
	})
}

// waiting are the cards that wait, the latest first.
func (e *Engine) waiting() []string {
	e.store.Lock()
	defer e.store.Unlock()
	var out []string
	for i := len(e.store.Messages) - 1; i >= 0; i-- {
		if e.store.Messages[i].Card.Waiting() {
			out = append(out, e.store.Messages[i].ID)
		}
	}
	return out
}

// View is what a view shows first.
func (e *Engine) View() loopy.View {
	e.mu.Lock()
	view := loopy.View{Host: e.host.Name(), Version: e.version, State: e.state, Label: e.label, Tool: e.calling, Running: e.run != nil, Used: e.used, Window: e.window}
	problem := e.problem
	e.mu.Unlock()
	view.Waiting = e.waiting()
	e.store.Lock()
	messages := e.store.Messages
	if len(messages) > page {
		messages, view.More = messages[len(messages)-page:], true
	}
	for _, message := range messages {
		view.Messages = append(view.Messages, copyMessage(message))
	}
	for _, task := range slices.Backward(e.store.Tasks) {
		task.Steps, task.PRs = slices.Clone(task.Steps), slices.Clone(task.PRs)
		view.Tasks = append(view.Tasks, task)
	}
	for _, routine := range e.store.Routines {
		routine.Runs = slices.Clone(routine.Runs)
		view.Routines = append(view.Routines, routine)
	}
	view.Rules = slices.Clone(e.store.Rules)
	for _, account := range e.store.Accounts {
		account.Password = ""
		view.Accounts = append(view.Accounts, account)
	}
	view.Settings = e.store.Settings
	e.store.Unlock()
	if e.safe != nil {
		view.Vault, view.VaultState, view.VaultStays = e.safe.list(), e.safe.State(), e.safe.Stays()
	}
	if problem != "" && view.Label == "" {
		view.State, view.Label = "blocked", problem
	}
	view.Facts = len(e.memory.Facts(false))
	view.Models = e.host.Models()
	view.Links = e.viewLinks()
	view.Browser = e.host.Page() != nil
	view.Taken = view.Browser && e.taken()
	if cfg, err := e.host.Config(view.Settings, e.folder.dir); err == nil {
		view.Model = cfg.Model
	}
	return view
}

const page = 120 // messages a view gets at once

// Before are the messages before one, oldest first.
func (e *Engine) Before(id string) (messages []loopy.Message, more bool) {
	e.store.Lock()
	defer e.store.Unlock()
	for i, message := range e.store.Messages {
		if message.ID == id {
			from := max(0, i-page)
			for _, m := range e.store.Messages[from:i] {
				messages = append(messages, copyMessage(m))
			}
			return messages, from > 0
		}
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// The conversation.

// Send is a message of the user's. While Loopy works it joins the work at
// its next step.
func (e *Engine) Send(text string, files []string) (loopy.Message, error) {
	text = strings.TrimSpace(text)
	if text == "" && len(files) == 0 {
		return loopy.Message{}, errors.New("nothing to send")
	}
	if len(text) > 200_000 {
		return loopy.Message{}, errors.New("that message is too long: send it as a file")
	}
	for _, file := range files {
		if _, err := clean(file); err != nil {
			return loopy.Message{}, err
		}
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return loopy.Message{}, errors.New("1loop is closing")
	}
	running := e.run
	// A run of Loopy's own lets the user's message come first, on its own.
	steer := running != nil && running.why == ""
	e.mu.Unlock()
	message := e.add(loopy.Message{From: "me", Text: text, Files: files, Pending: running != nil})
	if steer {
		e.mu.Lock()
		if e.run == running {
			running.steer = append(running.steer, message.ID)
			e.mu.Unlock()
			return message, nil
		}
		e.mu.Unlock()
	}
	e.enqueue(queued{text: text, message: message.ID}, true)
	return message, nil
}

// enqueue runs something, now or after what runs; the user's go first.
func (e *Engine) enqueue(q queued, first bool) {
	e.mu.Lock()
	if first {
		at := 0
		for at < len(e.queue) && e.queue[at].message != "" {
			at++
		}
		e.queue = slices.Insert(e.queue, at, q)
	} else {
		e.queue = append(e.queue, q)
	}
	e.mu.Unlock()
	e.next()
}

// next starts the first thing queued, when nothing runs.
func (e *Engine) next() {
	e.mu.Lock()
	if e.run != nil || len(e.queue) == 0 || e.closed {
		e.mu.Unlock()
		return
	}
	q := e.queue[0]
	e.queue = e.queue[1:]
	ctx, cancel := context.WithCancel(e.ctx)
	r := &run{cancel: cancel, why: q.why, routine: q.routine, link: q.link}
	e.run = r
	e.wait.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wait.Done()
		defer cancel()
		e.work(ctx, r, q)
	}()
}

// Stop ends what Loopy does.
func (e *Engine) Stop() {
	e.mu.Lock()
	r := e.run
	if r != nil {
		r.stopped = true
	}
	// What Loopy would do on its own stops too; the user's messages stay.
	e.queue = slices.DeleteFunc(e.queue, func(q queued) bool { return q.message == "" })
	e.mu.Unlock()
	if r != nil {
		r.cancel()
	}
}

// work is a run, from its message to its end.
func (e *Engine) work(ctx context.Context, r *run, q queued) {
	e.set("thinking", "Thinking")
	var files []string
	if q.message != "" {
		message, ok := e.update(q.message, func(m *loopy.Message) { m.Pending = false })
		if !ok { // cleared meanwhile
			e.finish(ctx, r, nil)
			return
		}
		files = message.Files
	}
	e.store.Lock()
	settings := e.store.Settings
	var history []agent.Message
	if len(e.store.History) > 0 {
		json.Unmarshal(e.store.History, &history)
	}
	skip := maps.Clone(e.store.Records)
	session, seenBy := e.store.Session, e.store.SeenBy
	e.store.Unlock()
	cfg, err := e.host.Config(settings, e.folder.dir)
	if err == nil {
		err = usable(cfg)
	}
	if err != nil {
		e.add(loopy.Message{From: "loopy", Text: err.Error()})
		e.finish(ctx, r, err)
		return
	}
	model := cfg.Provider + "/" + cfg.ConnectionID
	cfg.ClearSessions()
	if cfg.Provider == "claudecode" && session != "" && seenBy == model {
		cfg.SetClaudeSession(session)
	}
	if seenBy != "" && seenBy != model { // another model takes over: plain messages, which any reads
		history = plainHistory(history)
	}
	e.store.Lock()
	e.store.SeenBy = model
	e.store.Unlock()

	content := q.text
	if cfg.Provider == "claudecode" && session == "" || cfg.Provider == "claudecode" && seenBy != model {
		content = recent(history) + content // a new session has seen none of it
	}
	if len(files) > 0 {
		content += "\n\n[Files they sent, in your folder: " + strings.Join(files, ", ") + "]"
	}
	if recall := e.memory.Recall(q.text, skip); recall != "" && q.message != "" {
		content += "\n\n[From your memory, for this message. It may be out of date; use it only if it fits, and do not mention it for its own sake.]\n" + recall
	}
	content += "\n\n[" + e.now().Format("Monday 2 January 2006, 15:04") + "]"
	if q.message != "" {
		if record, err := e.memory.Add(memory.Record{User: q.text}); err == nil {
			r.record = record.ID
			e.store.Lock()
			if e.store.Records == nil {
				e.store.Records = map[string]bool{}
			}
			e.store.Records[record.ID] = true
			e.store.Unlock()
		}
	}
	history = append(history, agent.Message{Role: "user", Content: content, Internal: q.message == ""})

	cfg.ToolAllowed = allowed
	cfg.Assistant = &agent.AssistantTools{Prompt: e.prompt(r), Definitions: tools(e), Run: e.toolRun}
	cfg.Browser = e.guardBrowser(cfg.Browser)
	e.mu.Lock()
	e.window = agent.ModelContextWindow(cfg)
	e.mu.Unlock()
	inbox := agent.Inbox{
		Steer: func() []agent.Message { return e.takeSteer(r) },
		Next:  func() (agent.Message, bool) { return agent.Message{}, false },
	}
	err = e.host.Backend().Run(ctx, cfg, history, inbox, func(kind string, data any) { e.event(r, kind, data) })
	e.finish(ctx, r, err)
}

// usable says why a model cannot be Loopy's.
func usable(cfg agent.Config) error {
	switch cfg.Provider {
	case "antigravity", "chatgptweb":
		return fmt.Errorf("I can't work with %s: it only runs its own tools, and mine have to be used. Choose another model for me.", agent.ProviderName(cfg.Provider))
	}
	if cfg.Model == "" {
		return errors.New("Choose a model for me first.")
	}
	return nil
}

// plainHistory is what was said, without the tools' calls.
func plainHistory(history []agent.Message) []agent.Message {
	var out []agent.Message
	for _, m := range history {
		if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Content) != "" {
			out = append(out, agent.Message{Role: m.Role, Content: m.Content, Internal: m.Internal})
		}
	}
	return out
}

// recent is the end of the conversation, for a session that starts.
func recent(history []agent.Message) string {
	said := plainHistory(history)
	var b strings.Builder
	for _, m := range said[max(0, len(said)-16):] {
		who := "You"
		if m.Role == "user" {
			who = "User"
		}
		b.WriteString(who + ": " + firstWords(m.Content, 120) + "\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "[The end of your conversation so far]\n" + b.String() + "[Their new message]\n"
}

// takeSteer are the messages sent while Loopy worked.
func (e *Engine) takeSteer(r *run) []agent.Message {
	e.mu.Lock()
	ids := r.steer
	r.steer = nil
	e.mu.Unlock()
	var out []agent.Message
	for _, id := range ids {
		message, ok := e.update(id, func(m *loopy.Message) { m.Pending = false })
		if !ok {
			continue
		}
		content := message.Text
		if len(message.Files) > 0 {
			content += "\n\n[Files they sent, in your folder: " + strings.Join(message.Files, ", ") + "]"
		}
		e.memory.Add(memory.Record{User: message.Text})
		out = append(out, agent.Message{Role: "user", Content: content})
	}
	return out
}

// event is one of the agent's.
func (e *Engine) event(r *run, kind string, data any) {
	switch kind {
	case "delta", "text":
		text, _ := data.(string)
		if r.quiet && r.why != "" { // nothing to tell: nor this
			return
		}
		if r.stream == "" {
			if text == "" {
				return
			}
			r.stream = e.add(loopy.Message{From: "loopy", Text: text}).ID
		} else {
			e.update(r.stream, func(m *loopy.Message) {
				if kind == "text" {
					m.Text = text
				} else {
					m.Text += text
				}
			})
		}
		e.set("typing", "Writing to you")
	case "message_end":
		if r.stream == "" {
			return
		}
		id := r.stream
		r.stream = ""
		message, ok := e.message(id)
		text := strings.TrimSpace(message.Text)
		if !ok || text == "" {
			e.drop(id)
			return
		}
		if r.answer.Len() > 0 {
			r.answer.WriteString("\n")
		}
		r.answer.WriteString(text)
		// Text messages: one bubble a paragraph, unless it is a list or code.
		if parts := bubbles(text); len(parts) > 1 {
			e.update(id, func(m *loopy.Message) { m.Text = parts[0] })
			for _, part := range parts[1:] {
				e.add(loopy.Message{From: "loopy", Text: part})
			}
		} else if text != message.Text {
			e.update(id, func(m *loopy.Message) { m.Text = text })
		}
		r.said = true
	case "activity":
		activity, _ := data.(agent.Activity)
		state, label := activityOf(activity)
		tool := ""
		if activity.State == "call" {
			tool = activity.Tool
		}
		e.setCalling(state, label, tool)
	case "context_note":
		if text, ok := data.(string); ok && text != "" {
			e.add(loopy.Message{From: "loopy", Note: text})
			// The history no longer holds the earlier exchanges word for
			// word: the memory may bring them back.
			e.store.Lock()
			e.store.Records = nil
			if r.record != "" {
				e.store.Records = map[string]bool{r.record: true}
			}
			e.store.Unlock()
		}
	case "usage":
		if used, ok := data.(int); ok && used > 0 {
			e.mu.Lock()
			e.used = used
			e.mu.Unlock()
		}
	case "history":
		if history, ok := data.([]agent.Message); ok {
			if raw, err := json.Marshal(slim(history)); err == nil {
				e.store.Lock()
				e.store.History = raw
				e.store.Unlock()
				e.save()
			}
		}
	case "session":
		if session, ok := data.(string); ok {
			e.store.Lock()
			e.store.Session = session
			e.store.Unlock()
		}
	case "tool_result":
		if outcome, ok := data.(agent.ToolOutcome); ok && strings.HasPrefix(outcome.Name, "browser_") {
			e.tell(nil, nil, true)
		}
	}
}

// slim is the history without the pictures older than the last few, which
// the model has seen and the memory of 1loop need not keep.
func slim(history []agent.Message) []agent.Message {
	out := slices.Clone(history)
	left := 4
	for i := len(out) - 1; i >= 0; i-- {
		if len(out[i].Images) > 0 {
			if left > 0 {
				left--
				continue
			}
			out[i].Images = nil
			out[i].Content += "\n[picture no longer kept]"
		}
	}
	return out
}

// bubbles cuts an answer in text messages.
func bubbles(text string) []string {
	if strings.Contains(text, "```") || strings.Contains(text, "\n- ") || strings.Contains(text, "\n* ") || strings.Contains(text, "\n|") || strings.Contains(text, "\n1.") {
		return []string{text}
	}
	var out []string
	for _, part := range strings.Split(text, "\n\n") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) > 4 {
		return []string{text}
	}
	return out
}

// activityOf is the face's state for what the agent does.
func activityOf(activity agent.Activity) (state, label string) {
	switch activity.State {
	case "write":
		return "typing", "Writing to you"
	case "approval", "question":
		return "waiting", "Waiting for you"
	case "compact":
		return "thinking", "Tidying up my thoughts"
	case "call":
		return toolActivity(activity.Tool, activity.Detail)
	}
	return "thinking", "Thinking"
}

func toolActivity(tool, detail string) (state, label string) {
	switch tool {
	case "loopy_files":
		return "reading", "In my folder"
	case "loopy_run":
		return "copy", "Working something out"
	case "loopy_recall":
		return "reading", "Remembering"
	case "loopy_mail", "loopy_calendar":
		return "reading", "Reading your " + strings.TrimPrefix(tool, "loopy_")
	case "loopy_autolink_run":
		return "copy", "Running an AutoLink"
	case "loopy_inspect":
		return "reading", "Inspecting the page"
	case "loopy_sign_in":
		return "waiting", "Signing in"
	}
	if strings.HasPrefix(tool, "loopy_") {
		return "copy", "Getting things ready"
	}
	if strings.HasPrefix(tool, "browser_") {
		return "reading", agent.CallLabel(tool, detail)
	}
	return "copy", agent.CallLabel(tool, detail)
}

// finish closes a run; what waits starts next.
func (e *Engine) finish(ctx context.Context, r *run, err error) {
	if r.stream != "" { // cut while it wrote
		e.event(r, "message_end", nil)
	}
	stopped := ctx.Err() != nil
	for id, ch := range e.takeWaits() { // cards nobody waits for any more
		close(ch)
		e.update(id, func(m *loopy.Message) {
			if m.Card.Waiting() {
				m.Card.Answer, m.Card.Result = "Stopped", ""
			}
		})
	}
	answer := strings.TrimSpace(r.answer.String())
	if r.record != "" && answer != "" {
		e.memory.Amend(r.record, func(record *memory.Record) { record.Loopy = answer })
	}
	if r.routine != "" {
		outcome := "nothing to say"
		switch {
		case stopped:
			outcome = "stopped"
		case err != nil:
			outcome = agent.FirstLine(err.Error())
		case !r.quiet && answer != "":
			outcome = firstWords(answer, 24)
		case r.said && !r.quiet:
			outcome = "showed a card"
		}
		e.changeRoutine(r.routine, func(routine *loopy.Routine) {
			if n := len(routine.Runs); n > 0 {
				routine.Runs[n-1].Outcome = outcome
			}
		})
	}
	switch {
	case stopped:
		if r.stopped {
			e.add(loopy.Message{From: "loopy", Note: "Stopped"})
		}
		e.set("idle", "")
	case err != nil:
		if !errors.Is(err, errShown) {
			e.add(loopy.Message{From: "loopy", Note: "Something went wrong: " + agent.FirstLine(err.Error())})
		}
		e.set("blocked", "Something went wrong")
		e.rest(6 * time.Second)
	case r.why != "" && !r.quiet && r.said:
		e.set("ding", "Something new for you")
		e.notify("Loopy", firstWords(answer, 20), nil, false)
		e.rest(3 * time.Second)
	default:
		e.set("done", "Done")
		e.rest(3400 * time.Millisecond)
	}
	e.mu.Lock()
	// Messages that came too late to join it start the next run.
	var late []queued
	for _, id := range r.steer {
		if message, ok := e.messageUnder(id); ok {
			late = append(late, queued{text: message.Text, message: id})
		}
	}
	e.queue = append(late, e.queue...)
	e.run = nil
	e.mu.Unlock()
	e.tell(nil, nil, true)
	e.save()
	e.next()
	e.learnSoon()
}

func (e *Engine) messageUnder(id string) (loopy.Message, bool) { return e.message(id) }

var errShown = errors.New("shown")

func (e *Engine) takeWaits() map[string]chan loopy.Answer {
	e.mu.Lock()
	defer e.mu.Unlock()
	waits := e.waits
	e.waits = map[string]chan loopy.Answer{}
	return waits
}

// ask shows a card and waits for its answer.
func (e *Engine) ask(ctx context.Context, card loopy.Card) (loopy.Answer, error) {
	ch := make(chan loopy.Answer, 1)
	id := loopy.NewID()
	e.mu.Lock()
	e.waits[id] = ch
	if e.run != nil {
		e.run.said = true
	}
	e.mu.Unlock()
	message := e.add(loopy.Message{ID: id, From: "loopy", Card: &card})
	before := func() (string, string, string) { e.mu.Lock(); defer e.mu.Unlock(); return e.state, e.label, e.calling }
	state, label, tool := before()
	e.set("waiting", "Waiting for you")
	e.notify(card.Title, card.Body, &message, true)
	defer func() {
		e.mu.Lock()
		delete(e.waits, id)
		e.mu.Unlock()
	}()
	select {
	case answer, ok := <-ch:
		if !ok {
			return loopy.Answer{}, context.Canceled
		}
		e.setCalling(state, label, tool)
		return answer, nil
	case <-ctx.Done():
		e.update(id, func(m *loopy.Message) {
			if m.Card.Waiting() {
				m.Card.Answer = "Stopped"
			}
		})
		return loopy.Answer{}, ctx.Err()
	}
}

// notify tells the user, unless it is night and it can wait.
func (e *Engine) notify(title, text string, card *loopy.Message, urgent bool) {
	if !urgent && e.night(e.now()) {
		return
	}
	e.host.Notify(loopy.Notice{Title: title, Text: firstWords(text, 30), Card: card})
}

// notice is a notification as Loopy or a script made it: an urgent one
// comes at night too, a silent one makes no sound.
func (e *Engine) notice(notice loopy.Notice) {
	notice.Title, notice.Text = firstWords(notice.Title, 20), firstWords(notice.Text, 80)
	if notice.Title == "" {
		notice.Title = "Loopy"
	}
	if notice.Level != "urgent" && e.night(e.now()) {
		return
	}
	e.host.Notify(notice)
}

func (e *Engine) night(at time.Time) bool {
	e.store.Lock()
	from, to := e.store.Settings.QuietFrom, e.store.Settings.QuietTo
	e.store.Unlock()
	if from == "" && to == "" {
		from, to = "23:00", "07:00"
	}
	clock := at.Format("15:04")
	if from <= to {
		return from != to && clock >= from && clock < to
	}
	return clock >= from || clock < to
}

func firstWords(text string, n int) string {
	words := strings.Fields(text)
	if len(words) <= n {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:n], " ") + "…"
}

// Clear forgets the conversation (not the memory, the tasks, the routines).
func (e *Engine) Clear() {
	e.mu.Lock()
	e.queue = nil
	e.mu.Unlock()
	e.Stop()
	for range 100 { // the run ends
		e.mu.Lock()
		running := e.run != nil
		e.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.store.Lock()
	var gone []string
	for _, message := range e.store.Messages {
		gone = append(gone, message.ID)
	}
	e.store.Messages, e.store.History, e.store.Records, e.store.Session = nil, nil, nil, ""
	e.store.Unlock()
	e.mu.Lock()
	e.used = 0
	e.mu.Unlock()
	e.tell(nil, gone, true)
	e.save()
}

// React puts the user's reaction on a message of Loopy's.
func (e *Engine) React(id, reaction string) error {
	message, ok := e.update(id, func(m *loopy.Message) {
		if m.Reaction == reaction {
			reaction = ""
		}
		m.Reaction = reaction
	})
	if !ok {
		return errors.New("no such message")
	}
	if reaction != "" && message.From == "loopy" {
		e.store.Lock()
		var history []agent.Message
		if json.Unmarshal(e.store.History, &history) == nil && len(history) > 0 {
			history = append(history, agent.Message{Role: "user", Internal: true, Content: fmt.Sprintf("[The user reacted %s to your message %q. No answer is expected.]", loopy.Emoji(reaction), firstWords(message.Text, 20))})
			e.store.History, _ = json.Marshal(history)
		}
		e.store.Unlock()
	}
	return nil
}

// Set changes the settings.
func (e *Engine) Set(settings loopy.Settings) {
	e.store.Lock()
	settings.LastSummary = e.store.Settings.LastSummary
	e.store.Settings = settings
	e.store.Unlock()
	e.tell(nil, nil, true)
	e.save()
}

// log adds a line to the journal, and to the memory.
func (e *Engine) log(text string) {
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	e.store.Lock()
	off := e.store.Settings.JournalOff
	if !off {
		e.store.Journal = append(e.store.Journal, loopy.Entry{Time: e.now(), Text: text})
		if len(e.store.Journal) > 1000 {
			e.store.Journal = e.store.Journal[len(e.store.Journal)-1000:]
		}
	}
	e.store.Unlock()
	if !off {
		e.memory.Add(memory.Record{Event: text})
	}
	e.save()
}
