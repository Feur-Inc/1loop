package ui

// The handoff dialog: the first message after a change of provider, account
// or model asks how the new model receives the conversation (agent's
// handoff.go). The remote desktop shows the same dialog for its server.

import (
	"fmt"
	"sort"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

// pendingHandoff is the user's choice, applied when the turn starts.
type pendingHandoff struct {
	choice   agent.HandoffChoice
	plan     agent.HandoffPlan
	previous *agent.Config
}

// previousChoice is the model the chat last ran with, if still listed.
func (ui *desktopUI) previousChoice(chat *conversation) (agent.ModelChoice, bool) {
	ran := chat.Config.RanWith
	if ran == nil {
		return agent.ModelChoice{}, false
	}
	for _, choice := range ui.choices {
		if choice.Connection.ID == ran.ConnectionID && (ran.Model == "" || choice.ID == ran.Model) {
			return choice, true
		}
	}
	return agent.ModelChoice{}, false
}

// previousConfig is how to call that model again for the summary.
func (ui *desktopUI) previousConfig(chat *conversation) *agent.Config {
	choice, ok := ui.previousChoice(chat)
	if !ok {
		return nil
	}
	cfg := ui.runConfig(chat)
	previous := choice.Connection.Config(chat.Config.Workspace)
	previous.Model, previous.Effort, previous.ReasoningMode = choice.ID, choice.DefaultEffort, choice.ReasoningMode
	previous.Session, previous.SessionMark = chat.Config.Session, chat.Config.SessionMark
	previous.APIKey = ui.keys[previous.ConnectionID]
	previous.Cache, previous.ContextKey, previous.Todo, previous.Recall = cfg.Cache, cfg.ContextKey, cfg.Todo, cfg.Recall
	previous.UserInstructions, previous.Preferences, previous.ApprovalMode = cfg.UserInstructions, cfg.Preferences, agent.ApprovalFor(ui.approvalMode, previous)
	previous.Browser, previous.Terminal, previous.Computer, previous.Tasks = cfg.Browser, cfg.Terminal, cfg.Computer, cfg.Tasks
	previous.Phone, previous.Packages, previous.ProjectTools, previous.ToolAllowed = cfg.Phone, cfg.Packages, cfg.ProjectTools, cfg.ToolAllowed
	previous.Grants, previous.ClassicCompaction = cfg.Grants, cfg.ClassicCompaction
	return &previous
}

func (ui *desktopUI) previousName(chat *conversation) string {
	if choice, ok := ui.previousChoice(chat); ok {
		return choice.Display()
	}
	if ran := chat.Config.RanWith; ran != nil {
		if ran.Model != "" {
			return ran.Model
		}
		return ran.Provider
	}
	return "the previous model"
}

func (ui *desktopUI) modelName(cfg agent.Config) string {
	for _, choice := range ui.choices {
		if choice.Connection.ID == cfg.ConnectionID && choice.ID == cfg.Model {
			return choice.Display()
		}
	}
	if cfg.Model != "" {
		return cfg.Model
	}
	return cfg.Provider
}

// askHandoff shows the dialog for chat, then calls proceed with the choice.
func (ui *desktopUI) askHandoff(chat *conversation, proceed func()) {
	previous := ui.previousConfig(chat)
	next := ui.runConfig(chat)
	plan := agent.PlanHandoff(previous, next, chat.History, chat.Items, ui.previousName(chat), ui.modelName(next))
	labels := make([]string, 0, len(ui.choices))
	for label := range ui.choices {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		choice := ui.choices[label]
		candidate := choice.Connection.Config(chat.Config.Workspace)
		candidate.Model, candidate.APIKey = choice.ID, ui.keys[choice.Connection.ID]
		candidate.Effort, candidate.ReasoningMode = choice.DefaultEffort, choice.ReasoningMode
		if agent.IdentityOf(candidate) == agent.IdentityOf(next) || previous != nil && agent.IdentityOf(candidate) == agent.IdentityOf(*previous) {
			continue
		}
		plan.AddSummarizer("model:"+candidate.ConnectionID+"|"+candidate.Model, label, candidate, agent.HandoffAvailability(ui.modelUnavailable[candidate.ConnectionID], ui.accountLimits[candidate.ConnectionID]))
	}
	for i := range plan.Summarizers {
		entry := &plan.Summarizers[i]
		candidate := next
		if entry.ID == "previous" && previous != nil {
			candidate = *previous
		}
		if entry.ID == "previous" || entry.ID == "next" {
			if problem := agent.HandoffAvailability(ui.modelUnavailable[candidate.ConnectionID], ui.accountLimits[candidate.ConnectionID]); problem != "" {
				entry.Problem = problem
			}
		}
	}
	plan.RecommendAvailable()
	showHandoffDialog(ui.window, plan, func(choice agent.HandoffChoice) {
		if ui.current != chat || agent.IdentityOf(ui.runConfig(chat)) != agent.IdentityOf(next) {
			return
		}
		chat.handoff = &pendingHandoff{choice: choice, plan: plan, previous: previous}
		proceed()
	}, nil) // the prompt keeps its text until the message leaves
}

// showHandoffDialog is the dialog itself, for a plan made here or on a
// server.
func showHandoffDialog(window fyne.Window, plan agent.HandoffPlan, done func(agent.HandoffChoice), cancelled func()) {
	choice := plan.Recommended
	summarizerNames := map[string]string{}
	var summarizerLabels []string
	for _, summarizer := range plan.Summarizers {
		label := summarizer.Name
		switch {
		case summarizer.Problem != "":
			label += " (unavailable)"
		case summarizer.Cached:
			label += " (cached)"
		default:
			label += " (no cache)"
		}
		summarizerNames[label] = summarizer.ID
		summarizerLabels = append(summarizerLabels, label)
	}
	levelIDs := map[string]string{}
	var levelLabels []string
	for _, level := range plan.Levels {
		label := fmt.Sprintf("%s · ~%s tokens", level.Name, agent.Tokens(level.Tokens))
		levelIDs[label] = level.ID
		levelLabels = append(levelLabels, label)
	}
	intro := note(fmt.Sprintf("This conversation last ran with %s. %s starts without its cache: choose how it receives the conversation.", plan.From, plan.To))
	recommended := note("Recommended by 1loop: " + plan.Reason)
	asIs := note(plan.AsIsNote)
	summarizerNote, levelNote, problem := note(""), note(""), note("")
	summarizer := widget.NewSelect(summarizerLabels, nil)
	level := widget.NewSelect(levelLabels, nil)
	levelRow := container.NewVBox(widget.NewLabel("Level"), level, levelNote)
	summaryBox := container.NewVBox(widget.NewLabel("Summarized by"), summarizer, summarizerNote, levelRow)
	mode := widget.NewRadioGroup([]string{"Continue as is", "Summarize first"}, nil)
	var continueButton *tapRow
	var dialog *sheet
	refresh := func() {
		asIs.Hidden = choice.Mode != "as_is"
		summaryBox.Hidden = choice.Mode != "summary"
		cached := false
		for _, entry := range plan.Summarizers {
			if entry.ID == choice.Summarizer {
				summarizerNote.SetText(entry.Note)
				if entry.Problem != "" {
					summarizerNote.SetText(entry.Problem)
				}
				cached = entry.Cached
			}
		}
		levelRow.Hidden = cached // a cached model summarizes everything it holds
		for _, entry := range plan.Levels {
			if entry.ID == choice.Level {
				levelNote.SetText(entry.Description)
			}
		}
		text := plan.Problem(choice)
		problem.SetText(text)
		problem.Hidden = text == ""
		if continueButton != nil {
			if text == "" {
				continueButton.Enable()
			} else {
				continueButton.Disable()
			}
		}
		asIs.Refresh()
		summaryBox.Refresh()
		if dialog != nil {
			dialog.Refresh()
		}
	}
	mode.OnChanged = func(selected string) {
		if selected == "Continue as is" {
			choice.Mode = "as_is"
		} else {
			choice.Mode = "summary"
			if choice.Summarizer == "" {
				choice.Summarizer = "previous"
			}
			if choice.Level == "" {
				choice.Level = agent.LevelNoOutputs
			}
		}
		refresh()
	}
	summarizer.OnChanged = func(label string) { choice.Summarizer = summarizerNames[label]; refresh() }
	level.OnChanged = func(label string) { choice.Level = levelIDs[label]; refresh() }
	// The recommended choice, preselected.
	if choice.Level == "" {
		choice.Level = agent.LevelNoOutputs
	}
	if choice.Summarizer == "" {
		choice.Summarizer = "previous"
	}
	for label, id := range summarizerNames {
		if id == choice.Summarizer {
			summarizer.SetSelected(label)
		}
	}
	for label, id := range levelIDs {
		if id == choice.Level {
			level.SetSelected(label)
		}
	}
	if choice.Mode == "as_is" {
		mode.SetSelected("Continue as is")
	} else {
		mode.SetSelected("Summarize first")
	}
	chosen := false
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	continueButton = newRow("Continue", nil, stylePrimary, func() {
		if plan.Problem(choice) != "" {
			return
		}
		choice.Target = &plan.Target
		chosen = true
		dialog.dismiss()
		done(choice)
	})
	body := container.NewVBox(intro, recommended, mode, asIs, summaryBox, problem)
	refresh()
	scroll := container.NewVScroll(body)
	scroll.SetMinSize(fyne.NewSize(0, min(float32(480), window.Canvas().Size().Height*0.65)))
	dialog = showModal(window.Canvas(), "Continue with "+plan.To, scroll, 640, func() {
		if !chosen && cancelled != nil {
			cancelled()
		}
	}, cancel, continueButton)
}
