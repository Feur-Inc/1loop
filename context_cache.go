package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

type cacheRecord struct {
	Identity string    `json:"identity"`
	LastCall time.Time `json:"lastCall"`
}

// Cache timestamps belong to the chat, not the global connection: two live
// conversations must not refresh each other's inactivity estimates.
type contextCache struct {
	mu sync.Mutex
	cacheRecord
}

func (c *contextCache) snapshot() cacheRecord {
	if c == nil {
		return cacheRecord{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cacheRecord
}

func (c *contextCache) copy() *contextCache {
	if c == nil {
		return nil
	}
	return &contextCache{cacheRecord: c.snapshot()}
}

func (c *contextCache) MarshalJSON() ([]byte, error)    { return json.Marshal(c.snapshot()) }
func (c *contextCache) UnmarshalJSON(data []byte) error { return json.Unmarshal(data, &c.cacheRecord) }

func cacheIdentity(cfg config) string {
	// No API key or access token is included in persisted metadata.
	s := strings.Join([]string{cfg.ContextKey, cfg.Provider, cfg.BaseURL, cfg.ConnectionID, cfg.AuthProfile, cfg.AccountID, cfg.Model}, "\x00")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))
}

func (c *contextCache) record(cfg config, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cacheRecord = cacheRecord{Identity: cacheIdentity(cfg), LastCall: now}
}

func cacheStale(cfg config, now time.Time) string {
	r := cfg.Cache.snapshot()
	if r.LastCall.IsZero() {
		return "no saved cache record"
	}
	if r.Identity != cacheIdentity(cfg) {
		return "model, account or chat version changed"
	}
	if cfg.Provider == "vllm" {
		return ""
	} // LRU eviction, not a documented inactivity timer.
	// OpenAI API documents a 30-minute minimum for GPT-5.6 and later;
	// older retention modes vary. ChatGPT and arbitrary compatible servers
	// expose no expiry signal. One hour there is an inactivity heuristic,
	// never a claim that their server cache has definitely expired.
	ttl := time.Hour
	if strings.Contains(cfg.BaseURL, "api.openai.com") {
		if newerOpenAIModel(cfg.Model) {
			ttl = 30 * time.Minute
		} else if cfg.Parameters["prompt_cache_retention"] == "24h" || strings.HasPrefix(cfg.Model, "gpt-5.5") {
			ttl = 24 * time.Hour
		}
	}
	if now.Sub(r.LastCall) >= ttl {
		return "cache estimated stale after inactivity"
	}
	return ""
}

func newerOpenAIModel(model string) bool {
	var major, minor int
	if _, err := fmt.Sscanf(strings.TrimPrefix(model, "openai/"), "gpt-%d.%d", &major, &minor); err == nil {
		return major > 5 || major == 5 && minor >= 6
	}
	return strings.HasPrefix(model, "gpt-6")
}
