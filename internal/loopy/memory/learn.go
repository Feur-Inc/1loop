// SPDX-License-Identifier: MPL-2.0

//go:build !js

package memory

// Learning: from time to time a model reads what the user wrote since the
// last time and proposes facts. What it proposes is checked by code: each
// must come with the user's own words, found as they are in their message,
// so a fact is neither made up nor slipped in by a page or a mail.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Ask asks a model, with no tools.
type Ask func(ctx context.Context, system, prompt string) (string, error)

// Due says how many exchanges wait to be learnt from.
func (m *Memory) Due() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.unlearned())
}

func (m *Memory) unlearned() []Record {
	start := 0
	if at, ok := m.byID[m.state.Learned]; ok {
		start = at + 1
	}
	var out []Record
	for _, record := range m.records[start:] {
		if !record.Gone && (record.User != "" || record.Final != "") {
			out = append(out, record)
		}
	}
	return out
}

const learnSystem = `You keep the memory of a personal assistant named Loopy. From the user's messages you note what will still matter in weeks: how they want the assistant to act and answer, how they like things done, their habits, people, places, work and goals. You answer with JSON only.`

const learnRules = `Propose changes to the memory from the exchanges above.

Each change is one of:
{"do":"add","kind":K,"text":T,"quote":Q,"exchange":N,"told":B}
{"do":"replace","id":ID,"kind":K,"text":T,"quote":Q,"exchange":N,"told":B}   the user's words show the known fact ID changed or was wrong
{"do":"confirm","id":ID,"quote":Q,"exchange":N}   the user's words show it again
{"do":"end","id":ID,"quote":Q,"exchange":N}   no longer true, and nothing replaces it

K: "instruction" (how the assistant must act, a rule), "style" (how they like to be answered or texts written for them: length, tone, format, words), "preference", "habit" (what they regularly do or ask, and how), "person", "place", "work", "goal", "other".
T: one short sentence, complete on its own, in the language the user writes in, with the date when it matters ("Préfère les trains avant 10 h"). It guesses neither their gender nor what they did not say. One fact a change. A "replace" keeps what still holds of the old one ("meetings after 2pm" then "only on Tuesdays" gives "Meetings only on Tuesdays, after 2 pm").
Q: the user's own words that show it, copied exactly from one of their messages (or from the text they rewrote), at least three words.
B: true when their words say it outright (a rule, how they want things, a fact about them: "je mange pas de viande", "meetings only on Tuesdays", "pas de liste"); false when you infer it from what they do or ask.

Rules:
- Only lasting things. Not the task at hand, not a one-off request, not what the assistant or a document said.
- A correction of the assistant ("non, plus court", "je t'avais dit de ne pas…") is an instruction or a style: say the rule behind it, for next time.
- When the user rewrote a text the assistant proposed, what they changed shows how they like their texts: a "style".
- A rule keeps what it bears on: a wish about summaries is about summaries, not about every answer.
- Something inferred may be noted with told false; it guides little until seen again.
- Already known, or clearly the same: "confirm", never "add".
- Never a password, code, card or account number, ID number, or a health, political or religious detail they did not ask to be remembered. What they cannot or will not eat, allergies included, is kept: it steers what to propose.
- Nothing to change is common: {"changes":[]}.

Answer: {"changes":[…]}`

type change struct {
	Do, ID, Kind, Text, Quote string
	Exchange                  int
	Told                      bool
}

// Learned is what a round of learning changed.
type Learned struct {
	Added, Confirmed, Ended []Fact
	Refused                 []string // proposed, but not in the user's words
}

// Learn draws facts from the exchanges not read yet, at most batch of them.
func (m *Memory) Learn(ctx context.Context, ask Ask, batch int) (Learned, error) {
	m.mu.Lock()
	records := m.unlearned()
	if len(records) > batch {
		records = records[:batch]
	}
	if len(records) == 0 {
		m.mu.Unlock()
		return Learned{}, nil
	}
	var said strings.Builder
	for i, record := range records {
		fmt.Fprintf(&said, "## Exchange %d, %s\n", i+1, record.Time.Format("Mon 2006-01-02 15:04"))
		if record.User != "" {
			fmt.Fprintf(&said, "User: %s\n", clip(record.User, 1500))
		}
		if record.Final != "" {
			fmt.Fprintf(&said, "Loopy proposed this text:\n%s\nThe user sent this instead:\n%s\n", clip(record.Draft, 900), clip(record.Final, 900))
		}
		if record.Loopy != "" {
			fmt.Fprintf(&said, "Loopy (for context only): %s\n", clip(record.Loopy, 400))
		}
	}
	// The facts the exchanges could touch: the rules, and the closest ones.
	known := map[string]Fact{}
	for _, fact := range m.state.Facts {
		if fact.Current() && (fact.Kind == Instruction || fact.Kind == Style) {
			known[fact.ID] = fact
		}
	}
	for _, record := range records {
		for _, fact := range m.near(record.User+" "+record.Final, 8, false) {
			known[fact.ID] = fact
		}
	}
	var facts strings.Builder
	for _, fact := range m.state.Facts { // in their order
		if _, ok := known[fact.ID]; ok {
			fmt.Fprintf(&facts, "[%s] %s: %s\n", fact.ID, fact.Kind, fact.Text)
		}
	}
	if facts.Len() == 0 {
		facts.WriteString("(nothing yet)\n")
	}
	m.mu.Unlock()

	answer, err := ask(ctx, learnSystem, "# What is known of the user\n"+facts.String()+"\n# New exchanges\n"+said.String()+"\n"+learnRules)
	if err != nil {
		return Learned{}, err
	}
	var proposed struct{ Changes []change }
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start || json.Unmarshal([]byte(answer[start:end+1]), &proposed) != nil {
		return Learned{}, fmt.Errorf("the model did not answer with JSON")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var learned Learned
	for _, c := range proposed.Changes {
		if c.Exchange < 1 || c.Exchange > len(records) || !quoted(records[c.Exchange-1], c.Quote) {
			learned.Refused = append(learned.Refused, c.Text)
			continue
		}
		m.apply(c, records[c.Exchange-1], &learned)
	}
	m.state.Learned = records[len(records)-1].ID
	return learned, m.save()
}

var (
	passage = regexp.MustCompile("(?s)<pasted_content.*?(?:</pasted_content[^>]*>|$)|```.*?(?:```|$)|«[^»]{40,}(?:»|$)|“[^”]{40,}(?:”|$)|\"[^\"]{40,}\"")
	cited   = regexp.MustCompile(`(?m)^\s*>.*$`)
)

// own is the user's message without what they quote in it: a page or a mail
// pasted there is not their words.
func own(text string) string {
	return cited.ReplaceAllString(passage.ReplaceAllString(text, " | "), " | ")
}

// quoted says whether the words are the user's, in that exchange.
func quoted(record Record, quote string) bool {
	bare := func(s string) string {
		return strings.Join(strings.FieldsFunc(plain(s), func(r rune) bool { return !('a' <= r && r <= 'z' || '0' <= r && r <= '9') && r < 128 }), " ")
	}
	quote = bare(quote)
	if len(strings.Fields(quote)) < 2 && len(quote) < 8 {
		return false
	}
	return strings.Contains(" "+bare(own(record.User))+" ", " "+quote+" ") || strings.Contains(" "+bare(record.Final)+" ", " "+quote+" ")
}

func (m *Memory) apply(c change, record Record, learned *Learned) {
	find := func(id string) *Fact {
		for i := range m.state.Facts {
			if m.state.Facts[i].ID == id && m.state.Facts[i].Current() {
				return &m.state.Facts[i]
			}
		}
		return nil
	}
	fact := Fact{Text: strings.TrimSpace(c.Text), Kind: c.Kind, Said: c.Told, Quote: strings.TrimSpace(c.Quote), From: record.ID, Since: record.Time}
	switch c.Do {
	case "add", "replace":
		if fact.Text == "" || len(fact.Text) > 300 || secret(fact.Text) {
			learned.Refused = append(learned.Refused, "(not kept)")
			return
		}
		for _, tomb := range m.state.Tombs {
			if strings.Join(words(tomb), " ") == strings.Join(words(fact.Text), " ") || Embed(tomb).Close(Embed(fact.Text)) > .93 {
				return // the user had it forgotten
			}
		}
		replaces := ""
		if old := find(c.ID); c.Do == "replace" && old != nil {
			// What was noticed does not undo what was told.
			if old.Said && !fact.Said {
				return
			}
			replaces = old.ID
		}
		learned.Added = append(learned.Added, m.remember(fact, replaces))
	case "confirm":
		if old := find(c.ID); old != nil && old.From != record.ID {
			old.Seen, old.Times = record.Time, old.Times+1
			learned.Confirmed = append(learned.Confirmed, *old)
		}
	case "end":
		if old := find(c.ID); old != nil && (!old.Said || c.Told) {
			old.Until = record.Time
			learned.Ended = append(learned.Ended, *old)
		}
	}
}

// LearnNow says whether the learning may run: after some exchanges, or a while
// after the last one.
func (m *Memory) LearnNow(at time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	waiting := m.unlearned()
	if len(waiting) == 0 {
		return false
	}
	return len(waiting) >= 8 || at.Sub(waiting[len(waiting)-1].Time) >= 10*time.Minute
}
