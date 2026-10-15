package main

import "time"

type pairedDevice struct {
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash"`
	Added     time.Time `json:"added"`
}

type remoteChat struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	Running   bool   `json:"running"`
	Items     int    `json:"items"`
}

type remoteItem struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Images int    `json:"images,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Verb   string `json:"verb,omitempty"`
	Target string `json:"target,omitempty"`
	Mono   bool   `json:"mono,omitempty"`
	Meta   string `json:"meta,omitempty"`
	Tone   string `json:"tone,omitempty"` // of the meta: muted, added, danger, amber
	State  string `json:"state,omitempty"`
	Args   string `json:"args,omitempty"`
	Diff   string `json:"diff,omitempty"`
	Millis int64  `json:"millis,omitempty"`
	Failed string `json:"failed,omitempty"`
}

type remoteMeta struct {
	ContextStatus string          `json:"contextStatus,omitempty"`
	Chat          string          `json:"chat"`
	Title         string          `json:"title"`
	Running       bool            `json:"running"`
	Label         string          `json:"label,omitempty"`
	Mark          string          `json:"mark,omitempty"`
	Queue         []string        `json:"queue"`
	Steer         []string        `json:"steer"`
	Items         int             `json:"items"`
	Approval      *remoteApproval `json:"approval,omitempty"`
	// The chat's model, by its label in the model list, and context use.
	Model  string       `json:"model"`
	Effort string       `json:"effort"`
	Used   int          `json:"used"`
	Window int          `json:"window"`
	Todo   []remoteTodo `json:"todo"` // the chat's task list
}

type remoteApproval struct {
	Chat    string `json:"chat"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
}

type remoteEffort struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type remoteModel struct {
	Label   string         `json:"label"` // unique key, as in the desktop picker
	Name    string         `json:"name"`
	Detail  string         `json:"detail"`
	Efforts []remoteEffort `json:"efforts"`
}

// choiceLabel finds the picker label of a configuration's model.
type remoteTab struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	Index int    `json:"index,omitempty"` // a tool tab: its item in the conversation
}

type remoteTodo struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Started   int64  `json:"started,omitempty"` // Unix milliseconds
	Completed int64  `json:"completed,omitempty"`
}
