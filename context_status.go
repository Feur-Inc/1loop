package main

import (
	"fmt"
	"strings"
)

func taskContextText(cfg config, used, window int, classic bool, activity string) string {
	if window <= 0 {
		return "Context usage is available once a model is selected."
	}
	text := fmt.Sprintf("Context · %d%% · %s / %s tokens", min(100, used*100/window), tokens(used), tokens(window))
	if cfg.Provider == "claudecode" {
		return text + "\nCompaction is managed by Claude Code."
	}
	if activity == "compact" {
		return text + "\nCompacting context now…"
	}
	if classic {
		return text + fmt.Sprintf("\nNext: classic compaction from %.0f%%.", float64(contextThreshold(cfg, .85))*100/float64(window))
	}
	level, condition, fraction := 1, "when this prompt finishes", .4
	switch {
	case contextAt(cfg, used, .9):
		level, condition, fraction = 4, "at the next safe step boundary", .9
	case contextAt(cfg, used, .8):
		level, condition, fraction = 3, "at the model's next checkpoint", .8
	case contextAt(cfg, used, .7):
		level, condition, fraction = 2, "after the next completed task", .7
	}
	return text + fmt.Sprintf("\nNext eligible: level %d · %s (from %.0f%%).\nThe actual level depends on the next work boundary and available reduction.", level, condition, float64(contextThreshold(cfg, fraction))*100/float64(window))
}
func tokens(n int) string {
	switch {
	case n >= 1000000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprint(n)
}
