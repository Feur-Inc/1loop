// SPDX-License-Identifier: MPL-2.0

package agent

// Account limits: how much of a subscription's usage windows (5 hours, a
// week…) is left, read from the provider and shown beside the model picker.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type AccountLimitWindow struct {
	Label   string
	used    float64
	ResetAt time.Time
	// Group is the model group the window limits (Antigravity's "Gemini",
	// "Claude/GPT"…); empty, it limits every model of the account.
	Group string
}

type AccountLimits struct {
	Windows []AccountLimitWindow
	Note    string
	Checked time.Time
}

func (window AccountLimitWindow) Remaining() float64 {
	return math.Max(0, math.Min(100, 100-window.used))
}

func limitPeriod(seconds int64) string {
	switch {
	case seconds <= 0:
		return "Window"
	case seconds%86400 == 0:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds%3600 == 0:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds%60 == 0:
		return fmt.Sprintf("%dmin", seconds/60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func readAccountLimits(ctx context.Context, url string, headers http.Header, target any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header = headers
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target)
}

func (backend *App) ConnectionLimits(ctx context.Context, conn Connection, key string) (AccountLimits, error) {
	limits := AccountLimits{Note: "Limits not provided"}
	switch conn.Provider {
	case "openai":
		cfg, err := backend.openAIConfig(ctx, conn.Config(""))
		if err != nil {
			return limits, err
		}
		headers := http.Header{"Authorization": {"Bearer " + cfg.APIKey}, "User-Agent": {"1loop/" + openAIClientVersion}, "Originator": {"1loop"}}
		if cfg.AccountID != "" {
			headers.Set("ChatGPT-Account-Id", cfg.AccountID)
		}
		if cfg.Residency != "" {
			headers.Set("x-openai-internal-codex-residency", cfg.Residency)
		}
		type window struct {
			Used    *float64 `json:"used_percent"`
			Seconds int64    `json:"limit_window_seconds"`
			ResetAt int64    `json:"reset_at"`
		}
		type rateLimit struct {
			Primary   *window `json:"primary_window"`
			Secondary *window `json:"secondary_window"`
		}
		var data struct {
			RateLimit  rateLimit `json:"rate_limit"`
			Additional []struct {
				Name      string    `json:"limit_name"`
				RateLimit rateLimit `json:"rate_limit"`
			} `json:"additional_rate_limits"`
		}
		// The ChatGPT account endpoint is outside the Codex inference base URL.
		if err := readAccountLimits(ctx, "https://chatgpt.com/backend-api/wham/usage", headers, &data); err != nil {
			return limits, err
		}
		appendWindows := func(name string, rateLimitValue rateLimit) {
			for _, windowValue := range []*window{rateLimitValue.Primary, rateLimitValue.Secondary} {
				if windowValue == nil || windowValue.Used == nil {
					continue
				}
				label := limitPeriod(windowValue.Seconds)
				if name != "" {
					label = name + " · " + label
				}
				reset := time.Time{}
				if windowValue.ResetAt > 0 {
					reset = time.Unix(windowValue.ResetAt, 0)
				}
				limits.Windows = append(limits.Windows, AccountLimitWindow{Label: label, used: *windowValue.Used, ResetAt: reset})
			}
		}
		appendWindows("", data.RateLimit)
		for _, extra := range data.Additional {
			appendWindows(extra.Name, extra.RateLimit)
		}
	case "claudecode":
		// The connection's own Claude Code login (claude_accounts.go).
		data, err := os.ReadFile(filepath.Join(claudeDataDir(conn.ClaudeHome()), ".credentials.json"))
		if err != nil {
			return limits, err
		}
		var auth struct {
			OAuth struct {
				Access  string `json:"accessToken"`
				Expires int64  `json:"expiresAt"`
			} `json:"claudeAiOauth"`
		}
		if err := json.Unmarshal(data, &auth); err != nil {
			return limits, err
		}
		if auth.OAuth.Access == "" || auth.OAuth.Expires <= time.Now().UnixMilli() {
			limits.Note = "Claude session expired"
			return limits, nil
		}
		type window struct {
			Used    *float64  `json:"utilization"`
			ResetAt time.Time `json:"resets_at"`
		}
		var usage struct {
			FiveHour *window `json:"five_hour"`
			Week     *window `json:"seven_day"`
			Sonnet   *window `json:"seven_day_sonnet"`
			Opus     *window `json:"seven_day_opus"`
		}
		headers := http.Header{"Authorization": {"Bearer " + auth.OAuth.Access}, "Anthropic-Beta": {"oauth-2025-04-20"}}
		if err := readAccountLimits(ctx, "https://api.anthropic.com/api/oauth/usage", headers, &usage); err != nil {
			return limits, err
		}
		for _, item := range []struct {
			label       string
			windowValue *window
		}{{"5h", usage.FiveHour}, {"7d", usage.Week}, {"Sonnet · 7d", usage.Sonnet}, {"Opus · 7d", usage.Opus}} {
			if item.windowValue != nil && item.windowValue.Used != nil {
				limits.Windows = append(limits.Windows, AccountLimitWindow{Label: item.label, used: *item.windowValue.Used, ResetAt: item.windowValue.ResetAt})
			}
		}
	case "antigravity":
		return agyLimits(ctx)
	case "openrouter":
		cfg := conn.Config("")
		cfg.APIKey = key
		base, token, err := Endpoint(cfg)
		if err != nil {
			return limits, err
		}
		var data struct {
			Data struct {
				Daily *struct {
					Limit     float64  `json:"limit"`
					Remaining *float64 `json:"remaining"`
				} `json:"free_model_daily_requests"`
			} `json:"data"`
		}
		if err := readAccountLimits(ctx, base+"/key", http.Header{"Authorization": {"Bearer " + token}}, &data); err != nil {
			return limits, err
		}
		if d := data.Data.Daily; d != nil && d.Limit > 0 && d.Remaining != nil {
			limits.Windows = append(limits.Windows, AccountLimitWindow{Label: "Free/day", used: 100 * (1 - *d.Remaining/d.Limit)})
		}
	}
	return limits, nil
}

// appliesTo says whether the window limits model. A window without group
// limits every model. A group applies when the model's name contains one of
// its words ("Claude/GPT": claude or gpt); a model matching a named group
// ("Argon") is limited by that one, not by the family's ("Gemini"). A model
// no group names is limited by all of them.
func (window AccountLimitWindow) appliesTo(model string, all []AccountLimitWindow) bool {
	if window.Group == "" {
		return true
	}
	model = strings.ToLower(model)
	matches := func(group string) (named, family bool) {
		for _, word := range strings.FieldsFunc(strings.ToLower(group), func(r rune) bool { return r == '/' || r == ' ' }) {
			if strings.Contains(model, word) {
				if word == "gemini" {
					family = true
				} else {
					named = true
				}
			}
		}
		return named, family
	}
	anyNamed, anyMatch := false, false
	for _, other := range all {
		if other.Group == "" {
			continue
		}
		named, family := matches(other.Group)
		anyNamed = anyNamed || named
		anyMatch = anyMatch || named || family
	}
	if !anyMatch {
		return true
	}
	named, family := matches(window.Group)
	if anyNamed {
		return named
	}
	return family
}
