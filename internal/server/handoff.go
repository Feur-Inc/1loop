package server

// The handoff of a chat to another provider, account or model (the agent's
// handoff.go): POST /api/send answers 409 with the plan until the client
// sends the user's choice.

import (
	"sort"

	"1loop/internal/agent"
)

type pendingHandoff struct {
	choice   agent.HandoffChoice
	plan     agent.HandoffPlan
	previous *agent.Config
}

// previousChoiceLocked is the model the chat last ran with, if still listed.
func (server *headlessServer) previousChoiceLocked(chat *serverChat) (string, agent.ModelChoice, bool) {
	ran := chat.Config.RanWith
	if ran == nil {
		return "", agent.ModelChoice{}, false
	}
	for label, choice := range server.choices {
		if choice.Connection.ID == ran.ConnectionID && (ran.Model == "" || choice.ID == ran.Model) {
			return label, choice, true
		}
	}
	return "", agent.ModelChoice{}, false
}

func (server *headlessServer) modelLabelLocked(cfg agent.Config) string {
	for label, choice := range server.choices {
		if choice.Connection.ID == cfg.ConnectionID && choice.ID == cfg.Model {
			return label
		}
	}
	if cfg.Model != "" {
		return cfg.Model
	}
	return cfg.Provider
}

// handoffPlanLocked plans the handoff of chat to cfg, and how to call the
// previous model again for the summary.
func (server *headlessServer) handoffPlanLocked(chat *serverChat, cfg agent.Config, history []agent.Message) (agent.HandoffPlan, *agent.Config) {
	var previous *agent.Config
	from := "the previous model"
	if label, choice, ok := server.previousChoiceLocked(chat); ok {
		from = label
		prior := server.configFor(choice, chat.Config)
		prior.Effort = choice.DefaultEffort
		prior.APIKey = server.keys[prior.ConnectionID]
		prior.Session, prior.SessionMark = chat.Config.Session, chat.Config.SessionMark
		prior.Cache, prior.ContextKey, prior.Todo, prior.Recall = cfg.Cache, cfg.ContextKey, cfg.Todo, cfg.Recall
		prior.UserInstructions, prior.Preferences, prior.ApprovalMode = cfg.UserInstructions, cfg.Preferences, agent.ApprovalFor(server.approvalMode, prior)
		prior.Browser, prior.Terminal, prior.Tasks, prior.Phone, prior.ToolAllowed = cfg.Browser, cfg.Terminal, cfg.Tasks, cfg.Phone, cfg.ToolAllowed
		prior.Computer, prior.Packages, prior.ProjectTools = cfg.Computer, cfg.Packages, cfg.ProjectTools
		prior.Grants, prior.ClassicCompaction = cfg.Grants, cfg.ClassicCompaction
		previous = &prior
	} else if ran := chat.Config.RanWith; ran != nil && ran.Model != "" {
		from = ran.Model
	}
	items := append([]agent.ChatItem(nil), chat.Items...)
	plan := agent.PlanHandoff(previous, cfg, history, items, from, server.modelLabelLocked(cfg))
	labels := make([]string, 0, len(server.choices))
	for label := range server.choices {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		choice := server.choices[label]
		candidate := server.configFor(choice, cfg)
		if agent.IdentityOf(candidate) == agent.IdentityOf(cfg) || previous != nil && agent.IdentityOf(candidate) == agent.IdentityOf(*previous) {
			continue
		}
		candidate.Session, candidate.SessionMark = "", 0
		candidate.APIKey = server.keys[candidate.ConnectionID]
		plan.AddSummarizer("model:"+candidate.ConnectionID+"|"+candidate.Model, label, candidate, agent.HandoffAvailability("", server.limitsCache[candidate.ConnectionID]))
	}
	for i := range plan.Summarizers {
		entry := &plan.Summarizers[i]
		candidate := cfg
		if entry.ID == "previous" && previous != nil {
			candidate = *previous
		}
		if entry.ID == "previous" || entry.ID == "next" {
			if problem := agent.HandoffAvailability("", server.limitsCache[candidate.ConnectionID]); problem != "" {
				entry.Problem = problem
			}
		}
	}
	plan.RecommendAvailable()
	return plan, previous
}
