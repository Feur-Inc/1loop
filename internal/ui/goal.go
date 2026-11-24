// SPDX-License-Identifier: MPL-2.0

package ui

// The chat's goal on the desktop (#444, after Codex's /goal): the /goal
// command, the turns it starts, and its card at the top of the Tasks tab,
// with the time spent counting while the agent works on it. The goal itself
// is internal/agent/goal.go; a server's chats show the same card from their
// meta (remote_desktop.go).

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
)

// goalCommand runs a /goal in chat (nil: the start screen, where only a new
// goal starts a new chat): typed in the composer, which it then empties, or
// a goal card's button. It returns what went wrong, "" when it ran.
func (ui *desktopUI) goalCommand(chat *conversation, command agent.GoalCommand, typed bool) string {
	if chat == nil {
		if command.Action != "set" {
			return "This chat has no goal yet: /goal <objective> sets one and starts working on it."
		}
		if !ui.ready() {
			return ""
		}
		state := &agent.GoalState{}
		outcome, err := state.Command(command, false, ui.cfg.Provider)
		if err != nil {
			return err.Error()
		}
		ui.pendingGoal = state // the chat startIn makes takes it
		if typed {
			ui.prompt.clearSent()
		}
		ui.startIn(nil, *outcome.Start, "")
		ui.pendingGoal = nil
		if ui.current != nil {
			ui.goalUpdated(ui.current)
		}
		return ""
	}
	if chat.Goal == nil {
		chat.Goal = &agent.GoalState{}
	}
	if outcome := command.Action; (outcome == "set" || outcome == "resume" || outcome == "edit") && chat.run == nil && !ui.ready() {
		return ""
	}
	outcome, err := chat.Goal.Command(command, chat.run != nil, ui.cfg.Provider)
	if err != nil {
		return err.Error()
	}
	if typed {
		ui.prompt.clearSent()
	}
	if outcome.Start != nil {
		ui.startIn(chat, *outcome.Start, "") // its first line is the chat's note
	} else if outcome.Note != "" {
		ui.addItem(chat, agent.ChatItem{Kind: "note", Text: outcome.Note})
	}
	ui.goalUpdated(chat)
	return ""
}

// goalUpdated redraws the goal where it shows, opening the Tasks tab the
// first time the chat has one.
func (ui *desktopUI) goalUpdated(chat *conversation) {
	goal := chat.Goal.Snapshot()
	if goal != nil && goal.ID != chat.goalShown {
		chat.goalShown = goal.ID
		ui.panel.showTodo(chat)
	}
	ui.panel.todoChanged(chat)
	ui.save()
}

// drawGoalCard adds a goal's card to the top of a Tasks tab: the objective,
// its state, the time spent (the label returned, which ticks while the
// agent works), turns and tokens against their limits, and the controls,
// which run the /goal command they name.
func drawGoalCard(box *fyne.Container, goal *agent.Goal, run func(command string)) *canvas.Text {
	if goal == nil {
		return nil
	}
	title := textLine("Goal", 16, colText)
	title.TextStyle = fyne.TextStyle{Bold: true}
	status := agent.GoalStatusLabel(*goal)
	ink, fill := goalStatusColors(goal)
	head := container.NewHBox(title, container.NewCenter(tag(status, ink, fill)))
	if goal.By == "agent" {
		head.Add(container.NewCenter(tag("Set by the agent", colMuted, colRaised)))
	}
	objective := plainFlow(goal.Objective, 13.5, colText)
	clock := textLine(goalTimeText(goal, time.Now()), 12.5, colText)
	clock.TextStyle = fyne.TextStyle{Monospace: true}
	stats := container.NewHBox(goalStat(iconClock, clock))
	turns := goalTurnsText(goal)
	stats.Add(goalStat(nil, textLine(turns, 12.5, colMuted)))
	stats.Add(goalStat(nil, textLine(goalTokensText(goal), 12.5, colMuted)))
	rows := container.New(layout.NewCustomPaddedVBoxLayout(8), head, objective, stats)
	if goal.TokenBudget > 0 {
		rows.Add(goalMeter(float32(goal.TokensUsed) / float32(goal.TokenBudget)))
	}
	if goal.Reason != "" || goal.Idle {
		reason := goal.Reason
		if goal.Idle {
			reason = "The last turn made no progress: 1loop waits for you. Send a message, or resume."
		}
		rows.Add(plainFlow(reason, 12.5, colMuted))
	}
	// Clear keeps the right end: when the goal ends while the pointer is on
	// Pause, a click never lands on Clear instead.
	var main fyne.CanvasObject = layout.NewSpacer()
	switch {
	case goal.Status == agent.GoalActive && !goal.Idle:
		main = chip("Pause", func() { run("/goal pause") })
	case goal.Status != agent.GoalComplete && goal.Status != agent.GoalBudgetLimited:
		main = chip("Resume", func() { run("/goal resume") })
	}
	rows.Add(padded(2, 0, 0, 0, container.NewBorder(nil, nil, container.NewHBox(main), chip("Clear", func() { run("/goal clear") }))))
	box.Add(padded(0, 16, 0, 0, surface(padded(14, 14, 16, 16, rows), colSurface, colBorder, 12, 0)))
	return clock
}

func goalTurnsText(goal *agent.Goal) string {
	turns := map[bool]string{true: " turn", false: " turns"}[goal.Turns == 1]
	if goal.MaxTurns > 0 {
		return strconv.Itoa(goal.Turns) + " of " + strconv.Itoa(goal.MaxTurns) + " turns"
	}
	return strconv.Itoa(goal.Turns) + turns
}

func goalTokensText(goal *agent.Goal) string {
	if goal.TokenBudget > 0 {
		return agent.GoalTokens(goal.TokensUsed) + " of " + agent.GoalTokens(goal.TokenBudget) + " tokens"
	}
	return agent.GoalTokens(goal.TokensUsed) + " tokens"
}

// goalTimeText is the clock's text: the time spent, and the time limit.
func goalTimeText(goal *agent.Goal, now time.Time) string {
	text := agent.GoalDuration(goal.Spent(now))
	if goal.TimeBudget > 0 {
		text += " of " + agent.GoalDuration(time.Duration(goal.TimeBudget)*time.Second)
	}
	return text
}

func goalStat(icon fyne.Resource, label fyne.CanvasObject) fyne.CanvasObject {
	if icon == nil {
		return padded(0, 0, 0, 14, label)
	}
	glyph := canvas.NewImageFromResource(icon)
	glyph.SetMinSize(fyne.NewSquareSize(13))
	return padded(0, 0, 0, 14, container.NewHBox(container.NewCenter(glyph), label))
}

// goalMeter is the line that fills as the token budget is used.
func goalMeter(used float32) fyne.CanvasObject {
	track, fill := canvas.NewRectangle(colBorder), canvas.NewRectangle(colAmber)
	if used >= 1 {
		fill.FillColor = colDanger
	}
	track.CornerRadius, fill.CornerRadius = 1.5, 1.5
	return container.New(progressLayout{min(used, 1)}, track, fill)
}

func goalStatusColors(goal *agent.Goal) (color.NRGBA, color.NRGBA) {
	accent := toNRGBA(colAmber)
	soft := accent
	soft.A = 0x2a
	switch {
	case goal.Status == agent.GoalActive && !goal.Idle:
		return accent, soft
	case goal.Status == agent.GoalComplete:
		added := toNRGBA(colAdded)
		dim := added
		dim.A = 0x2a
		return added, dim
	case goal.Status == agent.GoalBlocked || goal.Status == agent.GoalBudgetLimited:
		danger := toNRGBA(colDanger)
		dim := danger
		dim.A = 0x2a
		return danger, dim
	}
	return toNRGBA(colMuted), toNRGBA(colRaised)
}

func toNRGBA(c color.Color) color.NRGBA {
	return color.NRGBAModel.Convert(c).(color.NRGBA)
}

// tickGoalClock updates a goal card's clock while the agent works on it.
func tickGoalClock(clock *canvas.Text, goal *agent.Goal) {
	if clock == nil || goal == nil {
		return
	}
	if text := goalTimeText(goal, time.Now()); text != clock.Text {
		clock.Text = text
		clock.Refresh()
	}
}

// goalButton runs a goal card's control in its chat, as typed.
func (ui *desktopUI) goalButton(chat *conversation, text string) {
	ui = ui.viewOf(chat)
	command, _ := agent.ParseGoalCommand(text)
	if problem := ui.goalCommand(chat, command, false); problem != "" {
		ui.notice("/goal", problem)
	}
}

// remoteGoal runs a phone's /goal: in its chat, or, for a new goal, in a
// new chat of workspace, as remoteSend starts one.
func (ui *desktopUI) remoteGoal(chatID, workspace string, command agent.GoalCommand) (string, string) {
	if chatID != "" {
		chat := ui.chatByID(chatID)
		if chat == nil {
			return "", "This chat no longer exists"
		}
		return chat.ID, ui.goalCommand(chat, command, false)
	}
	if command.Action != "set" {
		return "", "This chat has no goal yet: /goal <objective> sets one and starts working on it."
	}
	state := &agent.GoalState{}
	outcome, err := state.Command(command, false, ui.cfg.Provider)
	if err != nil {
		return "", err.Error()
	}
	ui.pendingGoal = state
	id, problem := ui.remoteSend("", "", workspace, *outcome.Start)
	ui.pendingGoal = nil
	if chat := ui.chatByID(id); chat != nil {
		ui.goalUpdated(chat)
	}
	return id, problem
}

// goalTitle is the title of a chat started by /goal: its objective's start.
func goalTitle(objective string) string {
	title := []rune(strings.Join(strings.Fields(objective), " "))
	if len(title) > 36 {
		title = title[:36]
	}
	return string(title)
}
