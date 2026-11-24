// SPDX-License-Identifier: MPL-2.0

// Package loopy is what Loopy, the personal agent, is made of, as its views
// see it (the desktop, the phone, the web client): messages, cards, tasks,
// routines, and how to reach it wherever it lives (client.go). What makes
// it work is in engine, memory and box.
package loopy

import "time"

// Message is one bubble, card or note of the conversation.
type Message struct {
	ID   string    `json:"id"`
	Time time.Time `json:"t"`
	// From is "me" (the user) or "loopy".
	From string `json:"from"`
	Text string `json:"text,omitempty"`
	// Reaction is the emoji the other side put on this message.
	Reaction string `json:"reaction,omitempty"`
	// Card replaces the bubble: a mail draft, a choice, an approval…
	Card *Card `json:"card,omitempty"`
	// Memory is a note under Loopy's bubbles: something it remembered
	// (with its fact's id, for Forget).
	Memory   string `json:"memory,omitempty"`
	MemoryID string `json:"memoryId,omitempty"`
	// Note is a quiet line in the conversation: a compaction, a routine
	// that ran.
	Note string `json:"note,omitempty"`
	// Files are names in Loopy's folder the message comes with.
	Files []string `json:"files,omitempty"`
	// Pending: the user's message waits for the run in progress.
	Pending bool `json:"pending,omitempty"`
	// Version orders the changes, for the views.
	Version int64 `json:"v,omitempty"`
}

// Card is something to act on, shown in the conversation and listed under
// "Waiting for you" while it waits.
type Card struct {
	// Kind is "mail", "message" (Slack…), "event", "choice", "approval",
	// "signin", "secret", "needs_you", "summary", "delegate" or "routine".
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
	Body     string   `json:"body,omitempty"`
	Options  []Option `json:"options,omitempty"`
	// Warning is said in red over the card: "This can't be undone".
	Warning string `json:"warning,omitempty"`
	// Actions are its buttons, the first one the main one.
	Actions []string `json:"actions,omitempty"`
	// Editable: the fields and the body can be changed before the main
	// button (a draft).
	Editable bool `json:"editable,omitempty"`
	// Ask is what the user types to answer (a code), and is masked when
	// Secret.
	Ask    string `json:"ask,omitempty"`
	Secret bool   `json:"secret,omitempty"`
	// Site is where a sign-in goes, and Keep offers Loopy's vault.
	Site string `json:"site,omitempty"`
	Keep bool   `json:"keep,omitempty"`
	// Answer is the button the user pressed, or the option chosen; the card
	// stops waiting then.
	Answer string `json:"answer,omitempty"`
	// Result says what came of it: "Sent to paul@example.com".
	Result string `json:"result,omitempty"`
	// Task, Routine are the delegated task or routine it is about.
	Task    string `json:"task,omitempty"`
	Routine string `json:"routine,omitempty"`
}

// Field is a labelled value of a card (To, Subject…).
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Option is one choice of a choice card, or one line of a summary with its
// action.
type Option struct {
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Aside  string `json:"aside,omitempty"`  // on the right: a price, a time
	Action string `json:"action,omitempty"` // a summary's line: its button
}

// Waiting says whether the card still waits for the user.
func (card *Card) Waiting() bool {
	return card != nil && card.Answer == "" && card.Kind != "summary" && (len(card.Actions)+len(card.Options) > 0 || card.Ask != "")
}

// Entry is one line of Loopy's journal: what it did, dated.
type Entry struct {
	Time time.Time `json:"t"`
	Text string    `json:"text"`
}

// Step is a moment of a coding task.
type Step struct {
	Time time.Time `json:"t"`
	Text string    `json:"text"`
}

// Task is a coding task Loopy handed to an agent in a project: a normal
// 1loop chat, which Loopy follows.
type Task struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Project string    `json:"project"`
	Brief   string    `json:"brief"`
	Chat    string    `json:"chat,omitempty"` // the project chat, once started
	Status  string    `json:"status"`         // "proposed", "running", "waiting", "done", "failed", "cancelled"
	Steps   []Step    `json:"steps,omitempty"`
	Result  string    `json:"result,omitempty"`
	PRs     []string  `json:"prs,omitempty"` // the pull requests it opened, as owner/name#N
	Time    time.Time `json:"t"`
}

// Routine is a task Loopy runs by itself: on a schedule ("daily 08:00",
// "weekdays 09:30", "mon,thu 18:00", "every 2h"), or when something
// happens (On).
type Routine struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	When  string `json:"when,omitempty"`
	Task  string `json:"task"`
	On    bool   `json:"on"`
	// Trigger is what starts it: "github" (Filter: owner/name issue|pr|ci),
	// "mail" (Filter: words of the sender or subject) or "webhook".
	Trigger string `json:"trigger,omitempty"`
	Filter  string `json:"filter,omitempty"`
	Token   string `json:"token,omitempty"` // a webhook's secret
	// Watch: it keeps an eye on something, at night too, and speaks only
	// when something needs the user.
	Watch   bool      `json:"watch,omitempty"`
	LastRun time.Time `json:"lastRun,omitzero"`
	Runs    []Run     `json:"history,omitempty"`
	Seen    string    `json:"seen,omitempty"` // the trigger's place
}

// Run is a time a routine ran.
type Run struct {
	Time    time.Time `json:"t"`
	Why     string    `json:"why"` // "schedule", "test", "github: issue #12 …"
	Outcome string    `json:"outcome,omitempty"`
}

// Rule is something the user always allows.
type Rule struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"` // what is allowed: "browser:send", "signin"…
	Site string    `json:"site,omitempty"`
	Time time.Time `json:"t"`
}

// VaultEntry is a sign-in or a secret as Loopy knows it: never its value.
type VaultEntry struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"` // "signin", "key", "contact"
	Site   string   `json:"site,omitempty"`
	Name   string   `json:"name"`
	Hint   string   `json:"hint,omitempty"` // when to use it: "work account"
	Fields []string `json:"fields,omitempty"`
}

// Account is a mail or calendar account, without its password.
type Account struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "mail" or "calendar"
	Address  string `json:"address"`
	IMAP     string `json:"imap,omitempty"`
	SMTP     string `json:"smtp,omitempty"`
	CalDAV   string `json:"caldav,omitempty"`
	Problem  string `json:"problem,omitempty"`
	Password string `json:"password,omitempty"` // only when it is set; never given back
}

// Settings are Loopy's own choices.
type Settings struct {
	Connection string `json:"connection,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	// Learner is the model that reads the exchanges for the memory, "" the
	// same; its connection is LearnerConnection.
	LearnerConnection string `json:"learnerConnection,omitempty"`
	Learner           string `json:"learner,omitempty"`
	// Summary is when the morning summary comes, "08:00", "off" for never.
	Summary     string    `json:"summary,omitempty"`
	LastSummary time.Time `json:"lastSummary,omitzero"`
	// Wants is what the user wants to hear about, in their words.
	Wants string `json:"wants,omitempty"`
	// QuietFrom and QuietTo are the night: only a watch that breaks
	// notifies. "23:00" and "07:00" by default; the same: no night.
	QuietFrom string `json:"quietFrom,omitempty"`
	QuietTo   string `json:"quietTo,omitempty"`
	// AskToRemember: Loopy remembers only what the user asks it to; else it
	// also keeps what clearly matters, and says so.
	AskToRemember bool `json:"askToRemember,omitempty"`
	// JournalOff: Loopy keeps no journal of what it did.
	JournalOff bool `json:"journalOff,omitempty"`
}

// Fact is one thing known of the user, for the memory page.
type Fact struct {
	ID     string    `json:"id"`
	Text   string    `json:"text"`
	Kind   string    `json:"kind"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until,omitzero"`
	Said   bool      `json:"said,omitempty"`
	Sure   bool      `json:"sure,omitempty"`
	Times  int       `json:"times,omitempty"`
	Pinned bool      `json:"pinned,omitempty"`
	Quote  string    `json:"quote,omitempty"`
}

// Kinds of facts, in the order the memory page lists them, and their names
// there.
var Kinds = []string{"instruction", "style", "preference", "habit", "person", "place", "work", "goal", "other"}

// KindNames are the memory page's titles.
var KindNames = map[string]string{"instruction": "Rules", "style": "How you like answers", "preference": "Preferences", "habit": "Habits", "person": "People", "place": "Places", "work": "Work", "goal": "Goals", "other": "Other"}

// MemoryView is the memory page.
type MemoryView struct {
	Facts   []Fact   `json:"facts"`
	Habits  []string `json:"habits,omitempty"` // how the user writes, counted
	Records int      `json:"records"`          // exchanges kept
	Waiting int      `json:"waiting"`          // not learnt from yet
	Journal []Entry  `json:"journal,omitempty"`
}

// File is one of Loopy's folder.
type File struct {
	Path string    `json:"path"`
	Dir  bool      `json:"dir,omitempty"`
	Size int64     `json:"size"`
	Time time.Time `json:"t"`
}

// Model is a model Loopy can use.
type Model struct {
	Connection string   `json:"connection"`
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Efforts    []string `json:"efforts,omitempty"`
}

// View is what a view of Loopy shows.
type View struct {
	Host     string       `json:"host"` // where it lives
	Version  int64        `json:"version"`
	Messages []Message    `json:"messages"` // the latest, oldest first
	More     bool         `json:"more,omitempty"`
	State    string       `json:"state"` // its face: "idle", "thinking", "reading", "copy", "typing", "waiting", "blocked", "done", "ding"
	Label    string       `json:"label,omitempty"`
	Running  bool         `json:"running"`
	Waiting  []string     `json:"waiting,omitempty"` // the cards that wait, the latest first
	Tasks    []Task       `json:"tasks,omitempty"`
	Routines []Routine    `json:"routines,omitempty"`
	Rules    []Rule       `json:"rules,omitempty"`
	Vault    []VaultEntry `json:"vault,omitempty"`
	Accounts []Account    `json:"accounts,omitempty"`
	Settings Settings     `json:"settings"`
	Model    string       `json:"model,omitempty"` // the one it uses now
	Models   []Model      `json:"models,omitempty"`
	Facts    int          `json:"facts"`
	Used     int          `json:"used,omitempty"`
	Window   int          `json:"window,omitempty"`
	Browser  bool         `json:"browser,omitempty"` // it has a page open
	Taken    bool         `json:"taken,omitempty"`   // the user took its browser over
}

// Change is what changed since a version.
type Change struct {
	Version  int64     `json:"version"`
	Messages []Message `json:"messages,omitempty"`
	Gone     []string  `json:"gone,omitempty"` // messages removed
	State    string    `json:"state"`
	Label    string    `json:"label,omitempty"`
	Running  bool      `json:"running"`
	Used     int       `json:"used,omitempty"`
	Window   int       `json:"window,omitempty"`
	// All: the rest of the view changed too (tasks, routines…): read it again.
	All bool `json:"all,omitempty"`
}

// Answer is the user's to a card.
type Answer struct {
	Message string            `json:"message"`
	Answer  string            `json:"answer"` // the button or the option
	Fields  []Field           `json:"fields,omitempty"`
	Body    string            `json:"body,omitempty"`
	Text    string            `json:"text,omitempty"`   // what Ask wanted
	Values  map[string]string `json:"values,omitempty"` // a sign-in's fields: never shown to Loopy
	Keep    bool              `json:"keep,omitempty"`   // in the vault
	Hint    string            `json:"hint,omitempty"`
}

// Reactions are the emojis of a reaction, by name.
var Reactions = []struct{ Name, Emoji string }{{"thumbs_up", "👍"}, {"heart", "❤️"}, {"done", "✅"}, {"laugh", "😄"}, {"wow", "😮"}, {"eyes", "👀"}, {"moon", "🌙"}, {"party", "🎉"}, {"sad", "😢"}, {"pray", "🙏"}}

// Emoji is a reaction's emoji; a text that is none is given back.
func Emoji(name string) string {
	for _, reaction := range Reactions {
		if reaction.Name == name {
			return reaction.Emoji
		}
	}
	return name
}
