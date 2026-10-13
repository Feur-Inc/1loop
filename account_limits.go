package main

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

	"fyne.io/fyne/v2"
)

type accountLimitWindow struct {
	label   string
	used    float64
	resetAt time.Time
}

type accountLimits struct {
	windows []accountLimitWindow
	note    string
	checked time.Time
}

func (w accountLimitWindow) remaining() float64 { return math.Max(0, math.Min(100, 100-w.used)) }

func (l accountLimits) summary() string {
	if len(l.windows) == 0 {
		return l.note
	}
	var parts []string
	for _, w := range l.windows[:min(2, len(l.windows))] {
		parts = append(parts, fmt.Sprintf("%s : %.0f%%", w.label, w.remaining()))
	}
	return strings.Join(parts, " · ")
}

func (l accountLimits) describe() string {
	if len(l.windows) == 0 {
		return l.note
	}
	lines := []string{"Remaining limits"}
	for _, w := range l.windows {
		line := fmt.Sprintf("%s : %.0f%%", w.label, w.remaining())
		if !w.resetAt.IsZero() {
			line += " · resets on " + w.resetAt.Local().Format("02/01 at 15:04")
		}
		lines = append(lines, line)
	}
	lines = append(lines, "Updated at "+l.checked.Local().Format("15:04"))
	return strings.Join(lines, "\n")
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

func (a *app) connectionLimits(ctx context.Context, c connection, key string) (accountLimits, error) {
	limits := accountLimits{note: "Limits not provided"}
	switch c.Provider {
	case "openai":
		cfg, err := a.openAIConfig(ctx, c.config(""))
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
		appendWindows := func(name string, r rateLimit) {
			for _, w := range []*window{r.Primary, r.Secondary} {
				if w == nil || w.Used == nil {
					continue
				}
				label := limitPeriod(w.Seconds)
				if name != "" {
					label = name + " · " + label
				}
				reset := time.Time{}
				if w.ResetAt > 0 {
					reset = time.Unix(w.ResetAt, 0)
				}
				limits.windows = append(limits.windows, accountLimitWindow{label, *w.Used, reset})
			}
		}
		appendWindows("", data.RateLimit)
		for _, extra := range data.Additional {
			appendWindows(extra.Name, extra.RateLimit)
		}
	case "claudecode":
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if dir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return limits, err
			}
			dir = filepath.Join(home, ".claude")
		}
		data, err := os.ReadFile(filepath.Join(dir, ".credentials.json"))
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
			limits.note = "Claude session expired"
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
			label string
			w     *window
		}{{"5h", usage.FiveHour}, {"7d", usage.Week}, {"Sonnet · 7d", usage.Sonnet}, {"Opus · 7d", usage.Opus}} {
			if item.w != nil && item.w.Used != nil {
				limits.windows = append(limits.windows, accountLimitWindow{item.label, *item.w.Used, item.w.ResetAt})
			}
		}
	case "openrouter":
		cfg := c.config("")
		cfg.APIKey = key
		base, token, err := endpoint(cfg)
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
			limits.windows = append(limits.windows, accountLimitWindow{label: "Free/day", used: 100 * (1 - *d.Remaining/d.Limit)})
		}
	}
	return limits, nil
}

func (u *desktopUI) loadAccountLimits(ctx context.Context, menu *sheet, c connection, show func(accountLimits)) {
	if cached, ok := u.accountLimits[c.ID]; ok && time.Since(cached.checked) < time.Minute {
		show(cached)
		return
	}
	if c.Provider != "openai" && c.Provider != "claudecode" && c.Provider != "openrouter" {
		show(accountLimits{note: "Limits not provided"})
		return
	}
	show(accountLimits{note: "Loading limits…"})
	key := u.keys[c.ID]
	go func() {
		limits, err := u.backend.connectionLimits(ctx, c, key)
		if err != nil {
			limits = accountLimits{note: "Limits unavailable"}
		}
		limits.checked = time.Now()
		fyne.Do(func() {
			if ctx.Err() != nil || u.model.open != menu || menu.closing {
				return
			}
			if u.accountLimits == nil {
				u.accountLimits = map[string]accountLimits{}
			}
			u.accountLimits[c.ID] = limits
			show(limits)
		})
	}()
}
