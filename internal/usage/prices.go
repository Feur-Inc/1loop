// SPDX-License-Identifier: MPL-2.0

package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Price is what a model costs, in US dollars per million tokens.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// Cost is what tokens cost at this price. Reasoning is already in Output.
func (price Price) Cost(tokens Tokens) float64 {
	return (float64(tokens.Input)*price.Input + float64(tokens.Output)*price.Output +
		float64(tokens.CacheRead)*price.CacheRead + float64(tokens.CacheWrite)*price.CacheWrite) / 1e6
}

// PricesAsOf is when the shipped table was last checked against the
// providers' price pages.
const PricesAsOf = "2026-11"

// shipped are the API list prices 1loop knows, by model as Key spells it.
// A model matches the longest entry it starts with, so a dated or suffixed
// id ("claude-sonnet-4-5-20250929", "gpt-5-codex") finds its family.
// Anthropic: cache writes are 1.25× input (5-minute cache), reads 0.1×.
var shipped = map[string]Price{
	// Anthropic
	"claude-fable-5-1":  {10, 50, 0.25, 12.5},
	"claude-mythos-5-1": {10, 50, 0.25, 12.5},
	"claude-fable-5":    {10, 50, 1, 12.5},
	"claude-mythos-5":   {10, 50, 1, 12.5},
	"claude-opus-5-5":   {4, 20, 0.2, 5},
	"claude-opus-5":     {5, 25, 0.5, 6.25},
	"claude-opus-4-8":   {5, 25, 0.5, 6.25},
	"claude-opus-4-7":   {5, 25, 0.5, 6.25},
	"claude-opus-4-6":   {5, 25, 0.5, 6.25},
	"claude-opus-4-5":   {5, 25, 0.5, 6.25},
	"claude-opus-4-1":   {15, 75, 1.5, 18.75},
	"claude-opus-4":     {15, 75, 1.5, 18.75},
	"claude-3-opus":     {15, 75, 1.5, 18.75},
	"claude-sonnet-5-5": {2, 10, 0.2, 2.5},
	"claude-sonnet-5":   {2, 10, 0.2, 2.5},
	"claude-sonnet-4":   {3, 15, 0.3, 3.75},
	"claude-3-7-sonnet": {3, 15, 0.3, 3.75},
	"claude-3-5-sonnet": {3, 15, 0.3, 3.75},
	"claude-haiku-5-5":  {0.1, 0.5, 0.01, 0.125},
	"claude-haiku-4-5":  {1, 5, 0.1, 1.25},
	"claude-3-5-haiku":  {0.8, 4, 0.08, 1},
	// OpenAI: cached input is read from the cache; writing it is free.
	"gpt-5-2":            {1.75, 14, 0.175, 0},
	"gpt-5-1":            {1.25, 10, 0.125, 0},
	"gpt-5-1-codex-mini": {0.25, 2, 0.025, 0},
	"gpt-5":              {1.25, 10, 0.125, 0},
	"gpt-5-codex-mini":   {0.25, 2, 0.025, 0},
	"gpt-5-mini":         {0.25, 2, 0.025, 0},
	"gpt-5-nano":         {0.05, 0.4, 0.005, 0},
	"gpt-5-pro":          {15, 120, 15, 0},
	"gpt-4-1":            {2, 8, 0.5, 0},
	"gpt-4-1-mini":       {0.4, 1.6, 0.1, 0},
	"gpt-4-1-nano":       {0.1, 0.4, 0.025, 0},
	"gpt-4o":             {2.5, 10, 1.25, 0},
	"gpt-4o-mini":        {0.15, 0.6, 0.075, 0},
	"o3":                 {2, 8, 0.5, 0},
	"o3-mini":            {1.1, 4.4, 0.55, 0},
	"o4-mini":            {1.1, 4.4, 0.275, 0},
	// Google (prompts up to 200k tokens)
	"gemini-3-pro":          {2, 12, 0.2, 0},
	"gemini-2-5-pro":        {1.25, 10, 0.125, 0},
	"gemini-2-5-flash":      {0.3, 2.5, 0.03, 0},
	"gemini-2-5-flash-lite": {0.1, 0.4, 0.01, 0},
	// DeepSeek
	"deepseek-chat":     {0.28, 0.42, 0.028, 0},
	"deepseek-reasoner": {0.28, 0.42, 0.028, 0},
}

var (
	dateSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)
	bracketTag = regexp.MustCompile(`\[[^\]]*\]$`)
)

// Key is how prices name a model: lower case, without the vendor prefix
// ("anthropic/"), a variant tag (":free", "[1m]"), "-latest" or a date,
// with dots as dashes ("gpt-5.1" is "gpt-5-1").
func Key(model string) string {
	key := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	if i := strings.Index(key, ":"); i >= 0 {
		key = key[:i]
	}
	key = bracketTag.ReplaceAllString(key, "")
	key = strings.ReplaceAll(key, ".", "-")
	key = strings.TrimSuffix(key, "-latest")
	key = dateSuffix.ReplaceAllString(key, "")
	return key
}

// Billing is how a provider's calls are paid: "api" by the token, "plan"
// by a subscription (the price shown is what the same tokens would cost
// on the API) or "local" on the user's own hardware (never priced).
func Billing(provider string) string {
	switch provider {
	case "openai", "claudecode", "antigravity", "chatgptweb":
		return "plan"
	case "vllm":
		return "local"
	}
	return "api"
}

// Prices are the shipped table with the user's own prices over it, kept
// beside the ledger.
type Prices struct {
	mu        sync.Mutex
	path      string
	overrides map[string]Price
}

// OpenPrices reads the user's prices at path.
func OpenPrices(path string) *Prices {
	prices := &Prices{path: path, overrides: map[string]Price{}}
	if data, err := os.ReadFile(path); err == nil {
		var saved struct {
			Overrides map[string]Price `json:"overrides"`
		}
		if json.Unmarshal(data, &saved) == nil && saved.Overrides != nil {
			for model, price := range saved.Overrides {
				prices.overrides[Key(model)] = price
			}
		}
	}
	return prices
}

// For finds a model's price: the most specific entry of the user's and
// the shipped table, the user's on a tie; ok is false for a model 1loop
// does not know, which is shown in tokens only.
func (prices *Prices) For(model string) (price Price, ok bool) {
	key := Key(model)
	if key == "" {
		return Price{}, false
	}
	entry, price, ok := match(shipped, key)
	if prices != nil {
		prices.mu.Lock()
		own, ownPrice, found := match(prices.overrides, key)
		prices.mu.Unlock()
		if found && len(own) >= len(entry) {
			return ownPrice, true
		}
	}
	return price, ok
}

func lookup(table map[string]Price, key string) (Price, bool) {
	_, price, ok := match(table, key)
	return price, ok
}

func match(table map[string]Price, key string) (string, Price, bool) {
	best := ""
	for entry := range table {
		if (key == entry || strings.HasPrefix(key, entry+"-")) && len(entry) > len(best) {
			best = entry
		}
	}
	if best == "" {
		return "", Price{}, false
	}
	return best, table[best], true
}

// Shipped is 1loop's own price for a model, ignoring the user's.
func Shipped(model string) (Price, bool) { return lookup(shipped, Key(model)) }

// Overrides are the user's prices, by model.
func (prices *Prices) Overrides() map[string]Price {
	prices.mu.Lock()
	defer prices.mu.Unlock()
	out := make(map[string]Price, len(prices.overrides))
	for model, price := range prices.overrides {
		out[model] = price
	}
	return out
}

// Set keeps the user's price for a model; nil goes back to the shipped one.
func (prices *Prices) Set(model string, price *Price) error {
	key := Key(model)
	prices.mu.Lock()
	if price == nil {
		delete(prices.overrides, key)
	} else {
		prices.overrides[key] = *price
	}
	data, _ := json.MarshalIndent(map[string]any{"overrides": prices.overrides}, "", "  ")
	path := prices.path
	prices.mu.Unlock()
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// Cost prices a call: what its provider reported, else the table's price
// for a provider paid by the token or by a plan. ok is false when the call
// cannot be priced (an unknown model, a local one).
func (prices *Prices) Cost(record Record) (cost float64, ok bool) {
	if record.Cost != nil {
		return *record.Cost, true
	}
	if Billing(record.Provider) == "local" {
		return 0, false
	}
	price, ok := prices.For(record.Model)
	if !ok {
		return 0, false
	}
	return price.Cost(record.Tokens), true
}

// PriceRow is a model as Settings lists it.
type PriceRow struct {
	Model   string `json:"model"`
	Price   *Price `json:"price,omitempty"`   // nil: unknown, tokens only
	Shipped *Price `json:"shipped,omitempty"` // 1loop's own, when the user changed it
	Custom  bool   `json:"custom,omitempty"`
}

// Table lists the models used (from the ledger) and the user's own, with
// their prices.
func (prices *Prices) Table(used []string) []PriceRow {
	seen := map[string]bool{}
	var rows []PriceRow
	add := func(model string) {
		key := Key(model)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		row := PriceRow{Model: key}
		if price, ok := prices.For(key); ok {
			row.Price = &price
		}
		prices.mu.Lock()
		_, row.Custom = prices.overrides[key]
		prices.mu.Unlock()
		if shippedPrice, ok := Shipped(key); ok && row.Custom {
			row.Shipped = &shippedPrice
		}
		rows = append(rows, row)
	}
	for _, model := range used {
		add(model)
	}
	for model := range prices.Overrides() {
		add(model)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Model < rows[j].Model })
	return rows
}
