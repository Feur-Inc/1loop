package agent

import (
	"slices"
	"testing"
)

func TestFamilyOf(t *testing.T) {
	for _, test := range []struct {
		id, resolved, family string
		version              []int
	}{
		{"gpt-5.4-mini", "", "GPT", []int{5, 4}},
		{"openai/gpt-4o", "", "GPT", []int{4}},
		{"openai/gpt-oss-120b", "", "GPT-OSS", nil},
		{"o3", "", "o-series", []int{3}},
		{"openai/o4-mini", "", "o-series", []int{4}},
		{"opus", "claude-opus-5", "Claude Opus", []int{5}},
		{"opus[1m]", "claude-opus-5[1m]", "Claude Opus", []int{5}},
		{"claude-fable-5-1[1m]", "claude-fable-5-1", "Claude Fable", []int{5, 1}},
		{"haiku", "claude-haiku-4-5-20251001", "Claude Haiku", []int{4, 5}},
		{"default", "claude-sonnet-5", "Default", nil},
		{"anthropic/claude-3.7-sonnet", "", "Claude Sonnet", []int{3, 7}},
		{"anthropic/claude-sonnet-4.5", "", "Claude Sonnet", []int{4, 5}},
		{"google/gemini-2.5-flash", "", "Gemini", []int{2, 5}},
		{"google/gemma-3-27b-it", "", "Gemma", []int{3}},
		{"/models/Qwen/Qwen3-8B", "", "Qwen", []int{3}},
		{"meta-llama/llama-3.3-70b-instruct", "", "Llama", []int{3, 3}},
		{"phi-4", "", "Phi", []int{4}},
	} {
		got := FamilyOf(ModelChoice{ID: test.id, Resolved: test.resolved})
		if got.Name != test.family || !slices.Equal(got.version, test.version) {
			t.Errorf("FamilyOf(%q) = %q %v, want %q %v", test.id, got.Name, got.version, test.family, test.version)
		}
	}
}

func TestSortModels(t *testing.T) {
	ids := func(choices []ModelChoice) []string {
		var out []string
		for _, choice := range choices {
			out = append(out, choice.ID)
		}
		return out
	}
	catalog := []ModelChoice{}
	for _, id := range []string{"anthropic/claude-sonnet-4.5", "openai/gpt-5.4-mini", "google/gemini-2.5-flash", "openai/o3",
		"anthropic/claude-opus-4.1", "openai/gpt-5.5", "google/gemini-3-pro-preview", "anthropic/claude-haiku-4.5", "openai/o4-mini",
		"anthropic/claude-opus-5", "openai/gpt-4.1", "anthropic/claude-sonnet-5", "openai/gpt-5.4", "anthropic/claude-3.7-sonnet",
		"mistralai/mistral-large", "openai/gpt-oss-120b"} {
		catalog = append(catalog, ModelChoice{ID: id})
	}
	SortModels(catalog)
	want := []string{"openai/gpt-5.5", "openai/gpt-5.4", "openai/gpt-5.4-mini", "openai/gpt-4.1",
		"openai/o4-mini", "openai/o3", "openai/gpt-oss-120b", "anthropic/claude-opus-5", "anthropic/claude-opus-4.1",
		"anthropic/claude-sonnet-5", "anthropic/claude-sonnet-4.5", "anthropic/claude-3.7-sonnet", "anthropic/claude-haiku-4.5",
		"google/gemini-3-pro-preview", "google/gemini-2.5-flash", "mistralai/mistral-large"}
	if got := ids(catalog); !slices.Equal(got, want) {
		t.Errorf("catalog order\n got %v\nwant %v", got, want)
	}

	// Claude Code's aliases sort by the model they stand for.
	claude := []ModelChoice{{ID: "default", Resolved: "claude-sonnet-5"}, {ID: "sonnet", Resolved: "claude-sonnet-5"},
		{ID: "claude-fable-5-1[1m]", Resolved: "claude-fable-5-1"}, {ID: "opus", Resolved: "claude-opus-5"},
		{ID: "haiku", Resolved: "claude-haiku-4-5-20251001"}, {ID: "opus[1m]", Resolved: "claude-opus-5[1m]"}}
	SortModels(claude)
	want = []string{"default", "claude-fable-5-1[1m]", "opus", "opus[1m]", "sonnet", "haiku"}
	if got := ids(claude); !slices.Equal(got, want) {
		t.Errorf("Claude Code order\n got %v\nwant %v", got, want)
	}
}
