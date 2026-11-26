// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"1loop/internal/core"
)

// CompactionState is what a chat keeps of its compactions (#128): the one
// that was interrupted, with the parts already summarized, and the last ones
// made, to compare the context before and after.
type CompactionState struct {
	mu sync.Mutex
	compactionData
}

type compactionData struct {
	Pending *PendingCompaction `json:"pending,omitempty"`
	Records []CompactionRecord `json:"records,omitempty"`
}

// PendingCompaction is a compaction that began and did not end.
type PendingCompaction struct {
	Level    int               `json:"level,omitempty"`
	Reason   string            `json:"reason,omitempty"`
	Focus    string            `json:"focus,omitempty"`
	Manual   bool              `json:"manual,omitempty"`
	Error    string            `json:"error,omitempty"` // "" while it runs, or when 1loop was closed
	Attempts int               `json:"attempts"`
	At       time.Time         `json:"at"`
	Total    int               `json:"total,omitempty"` // parts the summary is made of
	Parts    map[string]string `json:"parts,omitempty"` // the notes of those done, by the part's hash
}

// CompactionRecord is a compaction made: what the model had, what it has.
type CompactionRecord struct {
	At     time.Time          `json:"at"`
	Before int                `json:"before"`
	After  int                `json:"after"`
	Count  int                `json:"count"` // messages before
	Was    []CompactedMessage `json:"was"`
	Now    []CompactedMessage `json:"now"`
}

// CompactedMessage is a message of the context, abbreviated.
type CompactedMessage struct {
	Role   string `json:"role"`
	Text   string `json:"text"`
	Tokens int    `json:"tokens"`
}

const (
	maxCompactionRecords  = 3
	maxCompactionAttempts = 3
)

// Label is what the conversation's note says of a record, which finds it.
func (record CompactionRecord) Label() string {
	return fmt.Sprintf("~%d → ~%d tokens", record.Before, record.After)
}

func (state *CompactionState) snapshot() compactionData {
	if state == nil {
		return compactionData{}
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	data := compactionData{Records: append([]CompactionRecord(nil), state.Records...)}
	if state.Pending != nil {
		pending := *state.Pending
		pending.Parts = map[string]string{}
		for key, part := range state.Pending.Parts {
			pending.Parts[key] = part
		}
		data.Pending = &pending
	}
	return data
}

func (state *CompactionState) Copy() *CompactionState {
	if state == nil {
		return nil
	}
	return &CompactionState{compactionData: state.snapshot()}
}

func (state *CompactionState) MarshalJSON() ([]byte, error) { return json.Marshal(state.snapshot()) }
func (state *CompactionState) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &state.compactionData)
}

// Records are the last compactions, the oldest first.
func (state *CompactionState) List() []CompactionRecord { return state.snapshot().Records }

// Record is the compaction a note of the conversation tells, nil for none.
func FindCompaction(records []CompactionRecord, note string) *CompactionRecord {
	for i := len(records) - 1; i >= 0; i-- {
		if strings.Contains(note, records[i].Label()) {
			return &records[i]
		}
	}
	return nil
}

// Interrupted is the compaction to resume, nil for none.
func (state *CompactionState) Interrupted() *PendingCompaction { return state.snapshot().Pending }

// begin notes that a compaction starts, before its first request: closed in
// the middle, 1loop finds it. One already pending goes on.
func (state *CompactionState) begin(level int, reason, focus string, manual bool) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.Pending == nil {
		state.Pending = &PendingCompaction{Level: level, Reason: reason, Focus: focus, Manual: manual}
	}
	if state.Pending.Parts == nil {
		state.Pending.Parts = map[string]string{}
	}
	state.Pending.Attempts++
	state.Pending.Error, state.Pending.At = "", time.Now()
}

func (state *CompactionState) part(key string) string {
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.Pending == nil {
		return ""
	}
	return state.Pending.Parts[key]
}

// partsDone is how many of the keys are summarized already.
func (state *CompactionState) partsDone(keys []string) int {
	done := 0
	for _, key := range keys {
		if state.part(key) != "" {
			done++
		}
	}
	return done
}

func (state *CompactionState) keepPart(key, notes string, total int) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.Pending != nil {
		state.Pending.Parts[key], state.Pending.Total = notes, total
	}
}

// drop forgets the pending compaction: it is done, or cannot be.
func (state *CompactionState) drop() {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.Pending = nil
}

// interrupt keeps the pending compaction for later, and says so; after a few
// failures it gives up.
func (state *CompactionState) interrupt(reason string) string {
	if state == nil {
		return "Compaction failed; the original context was kept."
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	pending := state.Pending
	if pending == nil {
		return "Compaction failed; the original context was kept."
	}
	pending.Error = reason
	if pending.Attempts >= maxCompactionAttempts && reason != "stopped" {
		state.Pending = nil
		return fmt.Sprintf("Compaction failed %d times (%s); the original context was kept.", pending.Attempts, reason)
	}
	note := "Compaction interrupted (" + reason + "); the original context was kept."
	if done := len(pending.Parts); done > 0 && pending.Total > 0 {
		note += fmt.Sprintf(" %d of %d parts are summarized.", min(done, pending.Total), pending.Total)
	}
	return note + " It goes on where it stopped when it is next needed."
}

func (state *CompactionState) record(before, after int, was, now []Message) {
	if state == nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.Pending = nil
	state.Records = append(state.Records, CompactionRecord{At: time.Now(), Before: before, After: after, Count: len(was), Was: abbreviate(was, 400), Now: abbreviate(now, 12000)})
	if extra := len(state.Records) - maxCompactionRecords; extra > 0 {
		state.Records = append([]CompactionRecord(nil), state.Records[extra:]...)
	}
}

// abbreviate lists messages for the comparison, the middle of a very long
// list left out.
func abbreviate(messages []Message, limit int) []CompactedMessage {
	const most = 400
	var out []CompactedMessage
	for i, message := range messages {
		if len(messages) > most && i >= most/2 && i < len(messages)-most/2 {
			if i == most/2 {
				out = append(out, CompactedMessage{Role: "…", Text: fmt.Sprintf("%d messages", len(messages)-most)})
			}
			continue
		}
		role, text := message.Role, message.Content
		if message.Internal && role != "tool" {
			role = "1loop"
		}
		size := limit
		if userRequest(message) {
			size = min(limit, 600) // as the user wrote it, in the conversation
		}
		for _, call := range message.ToolCalls {
			text = strings.TrimSpace(text + "\n" + call.Function.Name + " " + core.TruncateText(call.Function.Arguments, 200))
		}
		if len(message.Responses) > 0 {
			text = "The provider's own compaction, which only it reads."
		}
		if len(message.Images) > 0 {
			text += fmt.Sprintf("\n[%d image(s)]", len(message.Images))
		}
		out = append(out, CompactedMessage{Role: role, Text: core.TruncateText(strings.TrimSpace(text), size), Tokens: tokenEstimate(message)})
	}
	return out
}

// ---------------------------------------------------------------------------
// What the summary is made from.

// digestLimits are the bytes a message keeps in the summary's input, at the
// shortest; everything is longer when the whole conversation leaves room.
const (
	digestUser    = 4000
	digestAnswer  = 2000
	digestCommand = 700
	digestResult  = 400
	digestNoise   = 160 // what was only read: the files are still there
	digestHandoff = 12000
)

var noisyTools = map[string]bool{"read_file": true, "list_directory": true, "search_files": true, "recall": true, "skill": true, "command_output": true, "browser_read": true, "terminal_read": true, "update_todo": true}

// middleOut keeps a text's start and, more, its end: an error is there.
func middleOut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	head := utf8Boundary(text, limit*2/5)
	tail := utf8Boundary(text, len(text)-limit*3/5)
	return text[:head] + "\n[…]\n" + text[tail:]
}

// digestMessages are the conversation's messages for the summary, without
// what it has no use for: 1loop's own reminders, what was only read,
// repeated outputs. scale lengthens what is kept.
func digestMessages(messages []Message, scale int) []string {
	names := map[string]string{}
	seen := map[string]bool{}
	var lines []string
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			names[call.ID] = call.Function.Name
		}
		text := strings.TrimSpace(message.Content)
		switch {
		case message.Role == "system" || message.Role == "developer":
			continue
		case message.Internal && message.Role == "user":
			continue // reminders, and what images came back
		case message.Role == "tool":
			name := names[message.ToolCallID]
			limit := digestResult * scale
			if noisyTools[name] {
				limit = digestNoise * scale
			} else if name == "shell" {
				limit = digestCommand * scale
			}
			if text == "" || seen[text] && len(text) > 200 {
				continue
			}
			seen[text] = true
			lines = append(lines, "RESULT "+name+": "+middleOut(text, limit))
			continue
		case userRequest(message):
			lines = append(lines, "USER: "+middleOut(text, digestUser*scale))
			continue
		}
		limit := digestAnswer * scale
		if strings.HasPrefix(text, "[Conversation handoff") {
			limit = max(limit, digestHandoff) // an earlier summary: its facts stay
		}
		if strings.HasPrefix(text, "[Recent activity, abbreviated") {
			limit = min(limit, digestAnswer*2)
		}
		var line strings.Builder
		if text != "" {
			line.WriteString("ASSISTANT: " + middleOut(text, limit))
		}
		for _, call := range message.ToolCalls {
			if line.Len() > 0 {
				line.WriteString("\n")
			}
			fmt.Fprintf(&line, "CALL %s(%s)", call.Function.Name, middleOut(call.Function.Arguments, digestResult*scale))
		}
		if line.Len() > 0 {
			lines = append(lines, line.String())
		}
	}
	return lines
}

// digestChunks is the conversation in parts of at most budget tokens, as
// detailed as one part allows.
func digestChunks(messages []Message, budget int) []string {
	var lines []string
	for _, scale := range []int{16, 8, 4, 2, 1} {
		lines = digestMessages(messages, scale)
		size := 0
		for _, line := range lines {
			size += len(line)/4 + 1
		}
		if size <= budget {
			return []string{strings.Join(lines, "\n")}
		}
	}
	var chunks []string
	var chunk strings.Builder
	for _, line := range lines {
		if chunk.Len() > 0 && (chunk.Len()+len(line))/4 > budget {
			chunks = append(chunks, chunk.String())
			chunk.Reset()
		}
		chunk.WriteString(middleOut(line, budget*4) + "\n")
	}
	if chunk.Len() > 0 {
		chunks = append(chunks, chunk.String())
	}
	return chunks
}

func chunkKey(focus, chunk string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(focus+"\x00"+chunk)))[:24]
}

// shortenRequests makes room when the user's own words, kept as written, are
// too many to compact (a long imported conversation, with pasted files): the
// longest of them, but not the last ones, keep their start and their end.
// The whole text stays in the saved conversation, for recall.
func shortenRequests(messages []Message, budget int) []Message {
	var long []int
	size, users := 0, 0
	for _, message := range messages {
		if userRequest(message) {
			size += tokenEstimate(message)
			users++
		}
	}
	index := 0
	for i, message := range messages {
		if !userRequest(message) {
			continue
		}
		index++
		if index <= users-2 && len(message.Content) > 1600 {
			long = append(long, i)
		}
	}
	if size <= budget || len(long) == 0 {
		return messages
	}
	out := append([]Message(nil), messages...)
	for _, limit := range []int{8000, 3000, 1500} {
		for _, i := range long {
			if size <= budget {
				return out
			}
			before := tokenEstimate(out[i])
			text := messages[i].Content
			if len(text) <= limit {
				continue
			}
			marker := fmt.Sprintf("\n[Shortened from %d bytes by the context compaction. Full message: recall.]\n", len(text))
			if id := messages[i].RecallID; id != "" {
				marker = fmt.Sprintf("\n[Shortened from %d bytes by the context compaction. Full message: recall read id=%s, offset=0.]\n", len(text), id)
			}
			head := utf8Boundary(text, limit*3/5)
			tail := utf8Boundary(text, len(text)-limit*2/5)
			out[i].Content = text[:head] + marker + text[tail:]
			size -= before - tokenEstimate(out[i])
		}
	}
	return out
}
