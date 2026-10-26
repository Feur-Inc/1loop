package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// AutoResume is the user's ordered response to a provider rate limit, for
// every conversation.
type AutoResume struct {
	Enabled bool         `json:"enabled"`
	Steps   []ResumeStep `json:"steps"`
}

type ResumeStep struct {
	Action       string `json:"action"` // wait or switch
	ConnectionID string `json:"connectionId,omitempty"`
	Model        string `json:"model,omitempty"`
	Effort       string `json:"effort,omitempty"`
	Handoff      string `json:"handoff,omitempty"` // as_is or summary
	Level        string `json:"level,omitempty"`
}

func (policy AutoResume) Validate() error {
	if len(policy.Steps) > 16 {
		return fmt.Errorf("Use at most 16 resume options.")
	}
	for _, step := range policy.Steps {
		switch step.Action {
		case "wait":
		case "switch":
			if step.ConnectionID == "" || step.Model == "" {
				return fmt.Errorf("Choose an account and a model.")
			}
			if step.Handoff != "as_is" && step.Handoff != "summary" {
				return fmt.Errorf("Choose how to transfer the conversation.")
			}
			if step.Handoff == "summary" && !Contains([]string{LevelEverything, LevelNoOutputs, LevelMessages, LevelRecent}, step.Level) {
				return fmt.Errorf("Choose what the summary includes.")
			}
		default:
			return fmt.Errorf("Unknown resume option.")
		}
	}
	if policy.Enabled && len(policy.Steps) == 0 {
		return fmt.Errorf("Add at least one resume option.")
	}
	return nil
}

// ResumeConfig keeps the run's tools and conversation state while selecting
// a connection and model already chosen by the user.
func ResumeConfig(current Config, choice ModelChoice, key, effort string) Config {
	next := current
	base := choice.Connection.Config(current.Workspace)
	next.Provider, next.ConnectionID, next.AuthProfile, next.BaseURL = base.Provider, base.ConnectionID, base.AuthProfile, base.BaseURL
	next.WebSettings, next.APIKey, next.Model = base.WebSettings, key, choice.ID
	next.AccountID, next.Residency = "", ""
	next.ReasoningMode, next.ServiceTier = choice.ReasoningMode, TierFor(choice, "")
	next.Effort = choice.DefaultEffort
	if Contains(choice.Efforts, effort) {
		next.Effort = effort
	}
	options := choice.Connection.ModelOptions[choice.ID]
	next.Routing = MergeOptions(base.Routing, options.Provider)
	next.Parameters = MergeOptions(base.Parameters, options.Parameters)
	next.ApprovalMode = ApprovalFor(current.ApprovalMode, next)
	return next
}

func ResumeStoredConfig(cfg Config) Config {
	return Config{
		ConnectionID: cfg.ConnectionID, AuthProfile: cfg.AuthProfile, Provider: cfg.Provider,
		BaseURL: cfg.BaseURL, Model: cfg.Model, Workspace: cfg.Workspace, Effort: cfg.Effort,
		ServiceTier: cfg.ServiceTier, ReasoningMode: cfg.ReasoningMode, Routing: cfg.Routing,
		Parameters: cfg.Parameters, Session: cfg.Session,
		SessionMark: cfg.SessionMark, RanWith: cfg.RanWith, WebConversation: cfg.WebConversation,
		WebSettings: cfg.WebSettings,
	}
}

// runWithResume retains the last emitted history; tool batches publish their
// results before another model request, so a retry continues from that point.
func (backend *App) runWithResume(ctx context.Context, cfg Config, history []Message, inbox Inbox, emit emitter) error {
	latest := append([]Message(nil), history...)
	var historyMu sync.Mutex
	session := cfg.Session
	forward := func(kind string, value any) {
		if kind == "history" {
			historyMu.Lock()
			latest = append([]Message(nil), value.([]Message)...)
			historyMu.Unlock()
		}
		if kind == "session" {
			if id, ok := value.(string); ok {
				historyMu.Lock()
				session = id
				historyMu.Unlock()
			}
		}
		emit(kind, value)
	}
	stepIndex := 0
	for {
		historyMu.Lock()
		input := append([]Message(nil), latest...)
		cfg.Session = session
		historyMu.Unlock()
		err := backend.runOnce(ctx, cfg, input, inbox, forward)
		historyMu.Lock()
		current := append([]Message(nil), latest...)
		cfg.Session = session
		historyMu.Unlock()
		var limit *RateLimitError
		if err == nil || ctx.Err() != nil || cfg.AutoResume == nil || !cfg.AutoResume.Enabled || !errors.As(err, &limit) {
			return err
		}
		resumed := false
		for stepIndex < len(cfg.AutoResume.Steps) {
			step := cfg.AutoResume.Steps[stepIndex]
			stepIndex++
			switch step.Action {
			case "wait":
				if limit.Reset.IsZero() {
					emit("context_note", "The provider did not report a reset time; trying the next configured option.")
					continue
				}
				wait := time.Until(limit.Reset)
				if wait < time.Second {
					wait = time.Second
				}
				// A duration, not a clock time: the phone's engine runs in UTC.
				emit("context_note", fmt.Sprintf("Rate limit reached on %s. Waiting %s before continuing.", cfg.Model, waitText(wait)))
				if waitErr := sleepContext(ctx, wait); waitErr != nil {
					return waitErr
				}
				resumed = true
			case "switch":
				if cfg.ResumeModel == nil {
					continue
				}
				next, resolveErr := cfg.ResumeModel(ctx, step)
				if resolveErr != nil {
					emit("context_note", "Resume model unavailable: "+resolveErr.Error())
					continue
				}
				emit("context_note", fmt.Sprintf("Rate limit reached on %s. Continuing with %s.", cfg.Model, next.Model))
				plan := PlanHandoff(&cfg, next, current, nil, cfg.Model, next.Model)
				choice := HandoffChoice{Mode: step.Handoff, Summarizer: "next", Level: step.Level}
				if choice.Mode == "" {
					choice.Mode = "as_is"
				}
				if choice.Mode == "summary" && choice.Level == "" {
					choice.Level = LevelEverything
				}
				result, handoffErr := backend.ApplyHandoff(ctx, &cfg, next, current, choice, plan, forward)
				if handoffErr != nil {
					emit("context_note", "Could not transfer context: "+handoffErr.Error())
					continue
				}
				next.AutoResume, next.ResumeModel, next.ResumeSwitched = cfg.AutoResume, cfg.ResumeModel, cfg.ResumeSwitched
				next.HandoffSummary, next.HandoffApplied = result.Summary, true
				if next.ResumeSwitched != nil {
					if err := next.ResumeSwitched(ctx, next); err != nil {
						return err
					}
				}
				cfg, current = next, result.History
				session = next.Session
				forward("context_note", result.Note)
				resumed = true
			}
			if resumed {
				break
			}
		}
		if !resumed {
			return err
		}
		current = append(current, Message{Role: "user", Internal: true, Content: "Continue the interrupted user request from the existing conversation and TODO. Do not repeat work already completed."})
		forward("history", current)
	}
}

// waitText is a wait as people say it: 15 s, 12 min, 3 h 20 min.
func waitText(wait time.Duration) string {
	switch {
	case wait < time.Minute:
		return fmt.Sprintf("%d s", int(wait.Round(time.Second)/time.Second))
	case wait < time.Hour:
		return fmt.Sprintf("%d min", int(wait.Round(time.Minute)/time.Minute))
	}
	wait = wait.Round(time.Minute)
	hours, minutes := int(wait/time.Hour), int(wait%time.Hour/time.Minute)
	if minutes == 0 {
		return fmt.Sprintf("%d h", hours)
	}
	return fmt.Sprintf("%d h %d min", hours, minutes)
}
