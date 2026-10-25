package agent

import (
	"slices"
	"strconv"
	"strings"
)

// ModelFamily is the line a model belongs to (GPT, o-series, Claude Opus…)
// and its version, so the pickers can group a provider's models.
type ModelFamily struct {
	Name    string
	rank    int
	version []int
}

// families are the known lines, in the order the pickers show them; the
// longest prefix that starts the model's name wins. Others follow, by name.
var families = []struct{ prefix, name string }{
	{"default", "Default"},
	{"gpt", "GPT"},
	{"chatgpt", "GPT"},
	{"o", "o-series"},
	{"gpt-oss", "GPT-OSS"},
	{"codex", "Codex"},
	{"claude-fable", "Claude Fable"},
	{"fable", "Claude Fable"},
	{"claude-opus", "Claude Opus"},
	{"opus", "Claude Opus"},
	{"claude-sonnet", "Claude Sonnet"},
	{"sonnet", "Claude Sonnet"},
	{"claude-haiku", "Claude Haiku"},
	{"haiku", "Claude Haiku"},
	{"gemini", "Gemini"},
	{"gemma", "Gemma"},
	{"grok", "Grok"},
	{"llama", "Llama"},
	{"mistral", "Mistral"},
	{"deepseek", "DeepSeek"},
	{"qwen", "Qwen"},
	{"glm", "GLM"},
	{"kimi", "Kimi"},
}

// FamilyOf reads a model's family and version from its ID (or, for a Claude
// Code alias such as "opus", the model it stands for). OpenRouter's vendor
// prefix, a local path, a context suffix ("[1m]") and a date are ignored.
func FamilyOf(choice ModelChoice) ModelFamily {
	id := choice.Resolved
	if id == "" || choice.ID == "default" {
		id = choice.ID
	}
	id = strings.ToLower(id)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if i := strings.Index(id, "["); i >= 0 {
		id = id[:i]
	}
	tokens := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' || r == ':' || r == ' ' })
	// Claude 3's names put the version first: claude-3-5-sonnet.
	if len(tokens) > 1 && tokens[0] == "claude" {
		for i, token := range tokens[1:] {
			if token == "opus" || token == "sonnet" || token == "haiku" || token == "fable" {
				tokens = append([]string{"claude", token}, slices.Delete(slices.Clone(tokens[1:]), i, i+1)...)
				break
			}
		}
	}
	name := strings.Join(tokens, "-")
	family, matched := ModelFamily{rank: len(families)}, ""
	for _, known := range families {
		if len(known.prefix) <= len(matched) || !strings.HasPrefix(name, known.prefix) {
			continue
		}
		rest := name[len(known.prefix):]
		// "o" is the o-series only before its number (o3, o4-mini), and a
		// prefix only counts at a word's end ("gemma", not "gem").
		if known.prefix == "o" && (rest == "" || rest[0] < '0' || rest[0] > '9') ||
			known.prefix != "o" && rest != "" && rest[0] != '-' && (rest[0] < '0' || rest[0] > '9') {
			continue
		}
		family.Name, matched = known.name, known.prefix
		family.rank = slices.IndexFunc(families, func(f struct{ prefix, name string }) bool { return f.name == known.name })
	}
	if family.Name == "" && len(tokens) > 0 {
		family.Name = strings.TrimRight(tokens[0], "0123456789.")
	}
	if family.Name == "" {
		family.Name = "Other"
	} else if family.rank == len(families) {
		family.Name = strings.ToUpper(family.Name[:1]) + family.Name[1:]
	}
	for _, token := range tokens {
		// The number may follow letters: o3, qwen3, v3, k2.
		token = strings.TrimLeft(token, "abcdefghijklmnopqrstuvwxyz")
		digits := strings.TrimLeft(token, "0123456789.")
		number := strings.Trim(token[:len(token)-len(digits)], ".")
		// Sizes (70b, 1m, 128k) and dates (20251001) are not versions.
		if number == "" || len(number) >= 6 || digits == "b" || digits == "m" || digits == "k" {
			continue
		}
		for _, part := range strings.Split(number, ".") {
			if value, err := strconv.Atoi(part); err == nil {
				family.version = append(family.version, value)
			}
		}
	}
	return family
}

// CompareModels orders models by family, then newest version first, then
// by ID, so a family's plain model comes before its variants (mini, 1M).
func CompareModels(left, right ModelChoice) int {
	a, b := FamilyOf(left), FamilyOf(right)
	if a.rank != b.rank {
		return a.rank - b.rank
	}
	if c := strings.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	// A model without a version (an alias) comes after the versioned ones.
	if (len(a.version) == 0) != (len(b.version) == 0) {
		if len(a.version) == 0 {
			return 1
		}
		return -1
	}
	if c := slices.Compare(b.version, a.version); c != 0 {
		return c
	}
	if c := len(left.ID) - len(right.ID); c != 0 {
		return c
	}
	return strings.Compare(left.ID, right.ID)
}

// SortModels orders a provider's models for the pickers (CompareModels).
func SortModels(choices []ModelChoice) {
	slices.SortStableFunc(choices, CompareModels)
}
