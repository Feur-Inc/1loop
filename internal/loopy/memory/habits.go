// SPDX-License-Identifier: MPL-2.0

package memory

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Habits are the user's ways of writing, counted from their messages
// without any model. The recent messages weigh more (Alpha), so a change
// of habit shows within a few dozen messages.
type Habits struct {
	Messages int                `json:"messages,omitempty"`
	Length   float64            `json:"length,omitempty"` // words a message
	Rates    map[string]float64 `json:"rates,omitempty"`  // signals, 0 to 1
	Shorts   map[string]int     `json:"shorts,omitempty"` // abbreviations used
	Hours    [24]int            `json:"hours,omitempty"`
	Asked    map[string]int     `json:"asked,omitempty"` // "shorter", "simpler"…: what they told Loopy about its answers
}

func (h Habits) copy() Habits {
	h.Rates, h.Shorts, h.Asked = maps.Clone(h.Rates), maps.Clone(h.Shorts), maps.Clone(h.Asked)
	return h
}

const habitAlpha = .06

var shorthands = strings.Fields("stp svp pk pq jsp mdr ptdr lol tkt bg bcp tjrs tt qqn qqch rdv dsl cad ds ok oki nan ouais wsh slt bjr bsr pls plz thx ty idk btw imo asap tbh u ur")

var frenchWords, englishWords = wordSet("le la les un une des du est et je tu il elle nous vous pour avec dans que qui pas ne sur mais stp ca c'est j'ai fait faire merci oui non quoi comment"), wordSet("the is and you i he she we for with in that this not on but please it's have do make thanks yes no what how can")

func wordSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range strings.Fields(text) {
		out[word] = true
	}
	return out
}

// asks are what users tell an assistant about how it answers.
var asks = map[string][]string{
	"shorter":  {"plus court", "trop long", "shorter", "too long", "en bref", "fais court", "be brief", "resume", "tl;dr"},
	"longer":   {"plus de detail", "plus detaille", "more detail", "developpe", "elaborate"},
	"simpler":  {"plus simple", "j'ai pas compris", "je comprend pas", "je comprends pas", "j'ai rien compris", "simpler", "i don't understand", "explique simplement", "eli5"},
	"tables":   {"fais un tableau", "un tableau", "in a table", "as a table"},
	"direct":   {"va droit au but", "sans blabla", "pas de blabla", "get to the point", "no fluff"},
	"no emoji": {"pas d'emoji", "sans emoji", "no emoji", "stop emoji"},
}

// note counts a message.
func (h *Habits) note(text string, at time.Time) {
	text = strings.TrimSpace(text)
	fields := strings.Fields(text)
	if len(fields) == 0 || len(fields) > 400 { // a pasted document says nothing of how they write
		return
	}
	if h.Rates == nil {
		h.Rates = map[string]float64{}
	}
	if h.Shorts == nil {
		h.Shorts = map[string]int{}
	}
	if h.Asked == nil {
		h.Asked = map[string]int{}
	}
	h.Messages++
	h.Hours[at.Hour()]++
	weight := max(habitAlpha, 1/float64(h.Messages))
	h.Length += weight * (float64(len(fields)) - h.Length)
	low := plain(text)
	french, english, emoji := 0, 0, false
	formal, familiar := false, false
	for _, word := range strings.FieldsFunc(low, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }) {
		if frenchWords[word] {
			french++
		}
		if englishWords[word] {
			english++
		}
		if slices.Contains(shorthands, word) {
			h.Shorts[word]++
		}
		switch word {
		case "vous", "votre", "vos":
			formal = true
		case "tu", "te", "toi", "ton", "tes", "ta":
			familiar = true
		}
	}
	accents := 0
	for _, r := range text {
		if r >= 0x1F300 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF {
			emoji = true
		}
		if strings.ContainsRune("éèêàâùûçîïôëü", unicode.ToLower(r)) {
			accents++
		}
	}
	first := []rune(text)[0]
	last := []rune(text)[len([]rune(text))-1]
	signal := func(name string, on bool) {
		value := 0.0
		if on {
			value = 1
		}
		h.Rates[name] += weight * (value - h.Rates[name])
	}
	signal("french", french > english)
	signal("english", english > french)
	signal("lower", unicode.IsLower(first))
	signal("bare", unicode.IsLetter(last) || unicode.IsDigit(last)) // no final punctuation
	signal("emoji", emoji)
	signal("exclaims", strings.Contains(text, "!"))
	signal("lists", strings.Contains(text, "\n- ") || strings.Contains(text, "\n1"))
	if french > english {
		signal("familiar", familiar && !formal)
		signal("formal", formal && !familiar)
		signal("accents", accents > 0)
	}
	for name, phrases := range asks {
		if slices.ContainsFunc(phrases, func(phrase string) bool { return strings.Contains(low, phrase) }) {
			h.Asked[name]++
		}
	}
}

// Describe says the habits in lines for Loopy's prompt, only what the
// counts show clearly.
func (h Habits) Describe() []string {
	if h.Messages < 6 {
		return nil
	}
	var out []string
	high := func(name string) bool { return h.Rates[name] >= .7 }
	switch {
	case high("french"):
		line := "They write in French"
		if high("familiar") || h.Rates["familiar"] >= .15 && h.Rates["formal"] < .05 {
			line += ", with \"tu\": answer with \"tu\""
		} else if h.Rates["formal"] >= .3 {
			line += ", with \"vous\": answer with \"vous\""
		}
		out = append(out, line+".")
	case high("english"):
		out = append(out, "They write in English.")
	case h.Rates["french"] >= .25 && h.Rates["english"] >= .25:
		out = append(out, "They write in French or English: answer in the language of the message.")
	}
	var manner []string
	switch {
	case h.Length < 10:
		manner = append(manner, "very short messages")
	case h.Length < 25:
		manner = append(manner, "short messages")
	case h.Length > 60:
		manner = append(manner, "long messages")
	}
	if high("lower") {
		manner = append(manner, "no capital at the start")
	}
	if high("bare") {
		manner = append(manner, "no final punctuation")
	}
	if high("french") && h.Rates["accents"] < .3 {
		manner = append(manner, "few accents")
	}
	if h.Rates["emoji"] >= .3 {
		manner = append(manner, "emojis")
	}
	if len(manner) > 0 {
		out = append(out, fmt.Sprintf("How they write: %s (about %.0f words a message). Keep the same informal tone, with correct spelling of your own.", strings.Join(manner, ", "), h.Length))
	}
	type count struct {
		word string
		n    int
	}
	var shorts []count
	for word, n := range h.Shorts {
		if n >= 3 {
			shorts = append(shorts, count{word, n})
		}
	}
	sort.Slice(shorts, func(i, j int) bool {
		return shorts[i].n > shorts[j].n || shorts[i].n == shorts[j].n && shorts[i].word < shorts[j].word
	})
	if len(shorts) > 0 {
		var names []string
		for _, s := range shorts[:min(6, len(shorts))] {
			names = append(names, s.word)
		}
		out = append(out, "Abbreviations they use, which you understand: "+strings.Join(names, ", ")+".")
	}
	var asked []string
	for name, n := range h.Asked {
		if n >= 2 {
			asked = append(asked, fmt.Sprintf("%s (%d times)", name, n))
		}
	}
	if len(asked) > 0 {
		sort.Strings(asked)
		out = append(out, "What they asked of your answers, more than once: "+strings.Join(asked, ", ")+". Do so from the start.")
	}
	if h.Messages >= 40 {
		best, sum := 0, 0
		for hour := range 24 {
			n := 0
			for k := range 4 {
				n += h.Hours[(hour+k)%24]
			}
			if n > sum {
				best, sum = hour, n
			}
		}
		if float64(sum) >= .5*float64(h.Messages) {
			out = append(out, fmt.Sprintf("They mostly write between %d:00 and %d:00.", best, (best+4)%24))
		}
	}
	return out
}
