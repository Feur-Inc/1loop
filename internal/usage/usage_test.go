// SPDX-License-Identifier: MPL-2.0

package usage

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var paris = time.FixedZone("CET", 3600)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestRangePeriods(t *testing.T) {
	now := time.Date(2026, 11, 11, 15, 30, 0, 0, paris) // a Wednesday
	cases := []struct {
		period   string
		offset   int
		from, to string
		label    string
	}{
		{"day", 0, "2026-11-11", "2026-11-12", "Today"},
		{"day", -1, "2026-11-10", "2026-11-11", "Yesterday"},
		{"day", -11, "2026-10-31", "2026-11-01", "Sat 31 Oct 2026"},
		{"week", 0, "2026-11-09", "2026-11-16", "This week"},
		{"week", -1, "2026-11-02", "2026-11-09", "Last week"},
		{"week", -2, "2026-10-26", "2026-11-02", "26 Oct – 1 Nov 2026"},
		{"month", 0, "2026-11-01", "2026-12-01", "This month"},
		{"month", -11, "2025-12-01", "2026-01-01", "December 2025"},
	}
	for _, c := range cases {
		from, to, label := Range(c.period, c.offset, now)
		if from.Format("2006-01-02") != c.from || to.Format("2006-01-02") != c.to || label != c.label {
			t.Errorf("%s %d: %s..%s %q, want %s..%s %q", c.period, c.offset, from.Format("2006-01-02"), to.Format("2006-01-02"), label, c.from, c.to, c.label)
		}
		if from.Location() != paris {
			t.Errorf("%s: bounds not in the local zone", c.period)
		}
	}
	// A Sunday belongs to the week that began on Monday.
	sunday := time.Date(2026, 11, 15, 23, 0, 0, 0, paris)
	if from, _, _ := Range("week", 0, sunday); from.Day() != 9 {
		t.Errorf("Sunday's week starts on %v", from)
	}
	if from, to, label := Range("all", 0, now); !from.IsZero() || !to.IsZero() || label != "All time" {
		t.Errorf("all: %v %v %q", from, to, label)
	}
}

func TestKeyAndPrices(t *testing.T) {
	for model, key := range map[string]string{
		"anthropic/claude-sonnet-4.5":  "claude-sonnet-4-5",
		"claude-opus-4-1-20250805":     "claude-opus-4-1",
		"claude-opus-5-5[1m]":          "claude-opus-5-5",
		"GPT-5.1-Codex":                "gpt-5-1-codex",
		"deepseek/deepseek-chat:free":  "deepseek-chat",
		"gemini-2.5-flash-lite-latest": "gemini-2-5-flash-lite",
	} {
		if got := Key(model); got != key {
			t.Errorf("Key(%q) = %q, want %q", model, got, key)
		}
	}
	prices := OpenPrices("")
	check := func(model string, input float64) {
		t.Helper()
		price, ok := prices.For(model)
		if !ok || !near(price.Input, input) {
			t.Errorf("%s: %+v %v, want input %v", model, price, ok, input)
		}
	}
	check("claude-sonnet-4-5-20250929", 3)
	check("claude-opus-4-5", 5) // not claude-opus-4's 15
	check("claude-opus-4-20250514", 15)
	check("gpt-5-mini", 0.25) // not gpt-5's
	check("gpt-5-codex", 1.25)
	check("openai/o3-mini", 1.1)
	if _, ok := prices.For("my-local-llama"); ok {
		t.Error("an unknown model got a price")
	}
	if _, ok := prices.For("gpt-50"); ok {
		t.Error("gpt-50 matched gpt-5")
	}
}

func TestPriceOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	prices := OpenPrices(path)
	if err := prices.Set("My-Model", &Price{Input: 1, Output: 2}); err != nil {
		t.Fatal(err)
	}
	if err := prices.Set("gpt-5", &Price{Input: 9}); err != nil {
		t.Fatal(err)
	}
	reopened := OpenPrices(path)
	if price, ok := reopened.For("my-model"); !ok || price.Output != 2 {
		t.Errorf("override lost: %+v %v", price, ok)
	}
	if price, _ := reopened.For("gpt-5"); price.Input != 9 {
		t.Errorf("override does not win over the shipped price: %+v", price)
	}
	if price, _ := reopened.For("gpt-5-mini"); price.Input != 0.25 {
		t.Errorf("a shorter override took a longer shipped entry: %+v", price)
	}
	reopened.Set("gpt-5", nil)
	if price, _ := reopened.For("gpt-5"); price.Input != 1.25 {
		t.Errorf("clearing the override: %+v", price)
	}
	table := reopened.Table([]string{"gpt-5", "unknown-x"})
	var unknown, custom bool
	for _, row := range table {
		unknown = unknown || row.Model == "unknown-x" && row.Price == nil
		custom = custom || row.Model == "my-model" && row.Custom
	}
	if !unknown || !custom {
		t.Errorf("table: %+v", table)
	}
}

func TestLedgerKeepsRecordsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	ledger := Open(path)
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	ledger.Add(Record{Time: at.Add(time.Hour), Model: "gpt-5", Tokens: Tokens{Input: 10}})
	ledger.Add(Record{Time: at, Model: "gpt-5", Tokens: Tokens{Input: 20}})
	ledger.Add(Record{Time: at, Model: "gpt-5"}) // no tokens: not a call
	// A write cut short is skipped, not fatal.
	file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	file.WriteString(`{"t":"2026-11-11T1`)
	file.Close()
	reopened := Open(path)
	records := reopened.Between(time.Time{}, time.Time{})
	if len(records) != 2 || records[0].Input != 20 || records[1].Input != 10 {
		t.Fatalf("records: %+v", records)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "content") {
		t.Error("the ledger holds more than numbers and ids")
	}
	if got := reopened.Between(at.Add(30*time.Minute), at.Add(2*time.Hour)); len(got) != 1 {
		t.Errorf("between: %+v", got)
	}
}

func seeded(now time.Time) *Ledger {
	ledger := Open("")
	add := func(at time.Time, record Record) {
		record.Time = at
		ledger.Add(record)
	}
	today := now.Add(-time.Hour)
	// A feature branch on Claude Code (a subscription), main on an API key.
	add(today, Record{Provider: "claudecode", Connection: "cc", Model: "claude-sonnet-4-5", Chat: "a", Turn: "a1", Project: "/src/app", Branch: "feature",
		Tokens: Tokens{Input: 1_000_000}})
	add(today.Add(time.Minute), Record{Provider: "claudecode", Connection: "cc", Model: "claude-sonnet-4-5", Chat: "a", Turn: "a2", Project: "/src/app", Branch: "feature",
		Tokens: Tokens{Output: 100_000, CacheRead: 1_000_000}})
	add(today, Record{Provider: "openrouter", Connection: "or", Model: "anthropic/claude-sonnet-4.5", Chat: "b", Turn: "b1", Project: "/src/app", Branch: "main",
		Tokens: Tokens{Input: 500_000}})
	// A model nobody priced, and a local one.
	add(today, Record{Provider: "compatible", Connection: "x", Model: "mystery-1", Chat: "b", Turn: "b1", Project: "/src/app", Branch: "main",
		Tokens: Tokens{Input: 7000, Output: 3000}})
	add(today, Record{Provider: "vllm", Connection: "v", Model: "gpt-5", Chat: "c", Turn: "c1", Project: "/src/other",
		Tokens: Tokens{Input: 1000}})
	// The provider's own price wins over the table.
	reported := 0.5
	add(today, Record{Provider: "openrouter", Connection: "or", Model: "gpt-5", Chat: "c", Turn: "c1", Project: "/src/other", Tokens: Tokens{Input: 1}, Cost: &reported})
	// Last week and last month.
	add(now.AddDate(0, 0, -7), Record{Provider: "openrouter", Connection: "or", Model: "gpt-5", Chat: "b", Project: "/src/app", Branch: "main", Tokens: Tokens{Input: 1_000_000}})
	add(now.AddDate(0, -1, 0), Record{Provider: "openrouter", Connection: "or", Model: "gpt-5", Chat: "d", Project: "/src/app", Tokens: Tokens{Output: 1_000_000}})
	return ledger
}

func TestBuildGroupsAndPrices(t *testing.T) {
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, paris)
	ledger, prices := seeded(now), OpenPrices("")
	names := func(group, key string) string {
		if group == "chat" && key == "a" {
			return "Fix the login"
		}
		return ""
	}
	day := Build(ledger, prices, names, Query{Period: "day", Group: "branch"}, now)
	// feature: 1M in at $3 + 100k out at $15 + 1M cache read at $0.30 = 3 + 1.5 + 0.3
	// main: 500k in at $3 = 1.5, plus mystery-1 unpriced
	// other: the reported 0.5; the local call is unpriced
	if !near(day.Totals.Cost, 4.8+1.5+0.5) {
		t.Errorf("day total %v", day.Totals.Cost)
	}
	if !near(day.Totals.PlanCost, 4.8) {
		t.Errorf("plan cost %v", day.Totals.PlanCost)
	}
	if day.Totals.Unpriced != 10000+1000 {
		t.Errorf("unpriced %v", day.Totals.Unpriced)
	}
	if day.Totals.Calls != 6 {
		t.Errorf("calls %d", day.Totals.Calls)
	}
	if len(day.Rows) != 3 || day.Rows[0].Label != "feature" || day.Rows[0].Detail != "app" || !near(day.Rows[0].Cost, 4.8) {
		t.Errorf("branch rows %+v", day.Rows)
	}
	if day.Rows[2].Label != "No branch" || day.Rows[2].Detail != "other" {
		t.Errorf("a folder without Git: %+v", day.Rows[2])
	}

	project := Build(ledger, prices, names, Query{Period: "day", Group: "project"}, now)
	if len(project.Rows) != 2 || project.Rows[0].Label != "app" || project.Rows[0].Calls != 4 {
		t.Errorf("project rows %+v", project.Rows)
	}

	model := Build(ledger, prices, names, Query{Period: "day", Group: "model"}, now)
	var mystery *Row
	for i, row := range model.Rows {
		if row.Key == "mystery-1" {
			mystery = &model.Rows[i]
		}
	}
	if mystery == nil || mystery.Cost != 0 || mystery.Unpriced != 10000 || !strings.Contains(mystery.Detail, "tokens only") {
		t.Errorf("unknown model row %+v", mystery)
	}
	// Both spellings of Sonnet 4.5 are one model.
	if model.Rows[0].Key != "claude-sonnet-4-5" || model.Rows[0].Calls != 3 {
		t.Errorf("model rows %+v", model.Rows)
	}

	chats := Build(ledger, prices, names, Query{Period: "day", Group: "chat"}, now)
	if chats.Rows[0].Label != "Fix the login" || chats.Rows[1].Label != "Deleted chat" {
		t.Errorf("chat rows %+v", chats.Rows)
	}

	turns := Build(ledger, prices, names, Query{Period: "all", Group: "turn", Chat: "a"}, now)
	if len(turns.Rows) != 2 || turns.Rows[0].Key != "a2" || !near(turns.Rows[0].Cost, 1.8) || turns.Totals.Calls != 2 {
		t.Errorf("turn rows %+v", turns.Rows)
	}

	connections := Build(ledger, prices, names, Query{Period: "day", Group: "connection"}, now)
	for _, row := range connections.Rows {
		if row.Key == "cc" && !strings.Contains(row.Detail, "API-equivalent") {
			t.Errorf("subscription not labelled: %+v", row)
		}
	}
}

func TestBuildCompareChartAndPace(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, paris)
	ledger, prices := seeded(now), OpenPrices("")
	previous := -1
	week := Build(ledger, prices, nil, Query{Period: "week", Group: "chat", Compare: &previous}, now)
	if week.Compare == nil || week.Compare.Label != "Last week" || !near(week.Compare.Totals.Cost, 1.25) {
		t.Fatalf("compare %+v", week.Compare)
	}
	var b *Row
	for i := range week.Rows {
		if week.Rows[i].Key == "b" {
			b = &week.Rows[i]
		}
	}
	if b == nil || b.Previous == nil || !near(b.Previous.Cost, 1.25) || !near(b.Cost, 1.5) {
		t.Errorf("row b %+v", b)
	}
	for _, row := range week.Rows {
		if row.Previous == nil {
			t.Errorf("row %s has no previous totals", row.Key)
		}
	}

	if len(week.Days) != 30 || week.Days[29].Date != "2026-11-11" || !near(week.Days[29].Cost, 6.8) || !near(week.Days[22].Cost, 1.25) {
		t.Errorf("days: last %+v, a week ago %+v", week.Days[29], week.Days[22])
	}

	month := Build(ledger, prices, nil, Query{Period: "month"}, now)
	// 10.5 days of 30 gone (11 Nov, noon): 6.8 + 1.25 spent.
	if month.Pace == nil || month.Pace.Day != 11 || month.Pace.Days != 30 || !near(month.Pace.Projected, (6.8+1.25)/(10.5/30)) {
		t.Errorf("pace %+v", month.Pace)
	}
	last := Build(ledger, prices, nil, Query{Period: "month", Offset: -1}, now)
	if !near(last.Totals.Cost, 10) || last.Pace.Projected != last.Pace.Spent || last.Days[29].Date != "2026-10-31" {
		t.Errorf("last month %+v %+v", last.Totals, last.Pace)
	}
	// A future period is the current one.
	if future := Build(ledger, prices, nil, Query{Period: "day", Offset: 3}, now); future.Label != "Today" {
		t.Errorf("future: %q", future.Label)
	}
}

func TestQueryEncoding(t *testing.T) {
	compare := -2
	query := Query{Period: "week", Offset: -1, Group: "chat", Chat: "a b", Compare: &compare}
	encoded := query.Encode()
	if !strings.Contains(encoded, "chat=a+b") || !strings.Contains(encoded, "compare=-2") {
		t.Errorf("encoded %q", encoded)
	}
}

func TestBranch(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".git"), 0700)
	os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/claude/342-usage\n"), 0600)
	sub := filepath.Join(root, "internal", "usage")
	os.MkdirAll(sub, 0700)
	if got := Branch(sub); got != "claude/342-usage" {
		t.Errorf("branch %q", got)
	}
	worktree := t.TempDir()
	gitDir := filepath.Join(root, ".git", "worktrees", "w")
	os.MkdirAll(gitDir, 0700)
	os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("0123456789abcdef\n"), 0600)
	os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0600)
	if got := Branch(worktree); got != "0123456" {
		t.Errorf("detached worktree %q", got)
	}
	if got := Branch(t.TempDir()); got != "" && !strings.Contains(os.TempDir(), got) {
		t.Errorf("outside Git %q", got)
	}
}

// A chat's total survives the chat (the ledger is apart from it) and adds
// its subchats up apart.
func TestChatSpend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	ledger := Open(path)
	ledger.Add(Record{Provider: "claudecode", Model: "claude-sonnet-4-5", Chat: "p", Tokens: Tokens{Input: 1_000_000}})
	ledger.Add(Record{Provider: "openrouter", Model: "gpt-5", Chat: "s", Parent: "p", Tokens: Tokens{Input: 1_000_000}})
	prices := OpenPrices("")
	spend := Open(path).ChatSpend(prices, "p")
	if spend == nil || !near(spend.Cost, 3) || spend.Subchats == nil || !near(spend.Subchats.Cost, 1.25) {
		t.Fatalf("spend %+v", spend)
	}
	if text := spend.Text(); !strings.Contains(text, "$3.00 API-equivalent") || !strings.Contains(text, "subchats: $1.25") {
		t.Errorf("text %q", text)
	}
	if ledger.ChatSpend(prices, "nobody") != nil {
		t.Error("a chat with no calls has a spend")
	}
	ledger.Add(Record{Provider: "compatible", Model: "mystery", Chat: "m", Tokens: Tokens{Input: 5000}})
	if text := ledger.ChatSpend(prices, "m").Text(); !strings.Contains(text, "5k tokens, no known price") {
		t.Errorf("unpriced text %q", text)
	}
}
