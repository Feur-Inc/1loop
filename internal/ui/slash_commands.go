package ui

// Slash commands in a Claude Code chat. Claude Code runs in its stream mode,
// where its interactive commands answer "isn't available in this
// environment" and an unknown one goes to the model as a prompt. So 1loop:
//   - handles the interactive ones itself (oneloopCommands);
//   - sends as they are the ones Claude Code lists in its handshake
//     (built-in commands, .claude/commands, skills, plugins);
//   - says so for any other, instead of sending it.
// Typing "/" in the composer offers them in a menu over the composer.

import (
	"fmt"
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
)

// commandOption is one line of the command menu.
type commandOption struct {
	name, hint, about string
}

// oneloopCommands are the commands 1loop runs itself in a Claude Code chat.
var oneloopCommands = []commandOption{
	{"status", "", "Show this connection's account, model and usage"},
	{"model", "[model]", "Choose the model"},
	{"clear", "", "Start a new chat with an empty context (this one stays in the sidebar)"},
	{"compact", "[instructions]", "Free up context by summarizing the conversation so far"},
	{"login", "", "Sign in to Claude Code"},
}

// oneloopAliases are other names of oneloopCommands, as in Claude Code.
var oneloopAliases = map[string]string{"reset": "clear", "new": "clear"}

// claudeUnavailable are Claude Code commands that cannot work in 1loop, and
// what to do instead.
var claudeUnavailable = map[string]string{
	"logout": "Sign Claude Code out from a terminal with claude auth logout.",
	"resume": "Open the chat from the sidebar to continue it.",
	"exit":   "Close the chat or 1loop instead.",
	"quit":   "Close the chat or 1loop instead.",
}

// matchCommands is the menu for what is typed after "/": names that start
// with it first, then names that contain it.
func matchCommands(options []commandOption, typed string) []commandOption {
	typed = strings.ToLower(typed)
	var first, then []commandOption
	for _, option := range options {
		name := strings.ToLower(option.name)
		if strings.HasPrefix(name, typed) {
			first = append(first, option)
		} else if strings.Contains(name, typed) {
			then = append(then, option)
		}
	}
	return append(first, then...)
}

// commandOptions are 1loop's commands, then those Claude Code accepts.
func commandOptions(info agent.ClaudeInfo) []commandOption {
	options := append([]commandOption(nil), oneloopCommands...)
	taken := map[string]bool{}
	for _, option := range options {
		taken[option.name] = true
	}
	for _, command := range info.Commands {
		if taken[command.Name] || oneloopAliases[command.Name] != "" || claudeUnavailable[command.Name] != "" {
			continue
		}
		taken[command.Name] = true
		options = append(options, commandOption{command.Name, command.ArgumentHint, agent.FirstLine(command.Description)})
	}
	return options
}

// commandsHere tells whether the composer's slash commands apply: a local
// Claude Code chat, not a message being edited.
func (ui *desktopUI) commandsHere() bool {
	return ui.server == nil && ui.cfg.Provider == "claudecode" && ui.editing == nil
}

// claudeConnection is the open chat's Claude Code connection.
func (ui *desktopUI) claudeConnection() agent.Connection {
	for _, conn := range ui.connectionsList {
		if conn.ID == ui.cfg.ConnectionID {
			return conn
		}
	}
	return agent.Connection{ID: ui.cfg.ConnectionID, Provider: "claudecode", Name: "Claude Code"}
}

// commandDir is the folder Claude Code lists commands in: the project, so
// its .claude/commands count, unless it is on an SSH machine.
func (ui *desktopUI) commandDir() string {
	if ui.cfg.MachineID != "" {
		return ""
	}
	return ui.cfg.Workspace
}

// loadClaudeInfo asks Claude Code for its commands and account in the
// background, then runs then on the interface's thread.
func (ui *desktopUI) loadClaudeInfo(then func(agent.ClaudeInfo, error)) {
	if ui.commandsLoading {
		return
	}
	ui.commandsLoading = true
	conn, dir := ui.claudeConnection(), ui.commandDir()
	go func() {
		info, err := ui.backend.ClaudeInfo(ui.ctx, conn, dir)
		fyne.Do(func() {
			ui.commandsLoading = false
			ui.commandsErr = err
			then(info, err)
		})
	}()
}

// slashCommand runs a slash command typed in a Claude Code chat. It returns
// false when the prompt goes to Claude Code as it is.
func (ui *desktopUI) slashCommand(name, args string) bool {
	if alias := oneloopAliases[name]; alias != "" {
		name = alias
	}
	switch name {
	case "status":
		ui.prompt.clearSent()
		ui.showClaudeStatus()
		return true
	case "model":
		ui.prompt.clearSent()
		ui.modelCommand(args)
		return true
	case "login":
		ui.prompt.clearSent()
		ui.signInClaude(ui.claudeConnection(), func() {})
		return true
	case "clear":
		ui.prompt.clearSent()
		ui.startNew()
		return true
	case "compact":
		if ui.current != nil && ui.cfg.Session != "" {
			return false // Claude Code compacts its own session
		}
		ui.prompt.clearSent()
		ui.notice("Compact", "Claude Code has no session in this chat yet, so there is nothing to compact. Your next message starts one, with a summary of the earlier conversation.")
		return true
	}
	if reason := claudeUnavailable[name]; reason != "" {
		ui.notice("/"+name, "/"+name+" isn't available in 1loop. "+reason)
		return true
	}
	info, ok := ui.backend.CachedClaudeInfo(ui.claudeConnection(), ui.commandDir())
	if !ok {
		// Known once Claude Code has listed its commands: try again then.
		ui.loadClaudeInfo(func(_ agent.ClaudeInfo, err error) {
			if err != nil {
				ui.notice("Slash commands", "Claude Code did not list its commands: "+err.Error())
				return
			}
			if command, _, ok := agent.SlashCommand(ui.prompt.Text); ok && command == name {
				ui.submit()
			}
		})
		return true
	}
	if _, ok := info.Command(name); ok {
		return false
	}
	ui.notice("Unknown command", fmt.Sprintf("/%s isn't a command Claude Code accepts here, so nothing was sent. Type / to see the available ones.", name))
	return true
}

// modelCommand is /model: with a name, the matching model of this
// connection; without one (or none matching), the model picker.
func (ui *desktopUI) modelCommand(wanted string) {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	if wanted != "" {
		var found []agent.ModelChoice
		for _, choice := range ui.choices {
			if choice.Connection.ID != ui.cfg.ConnectionID {
				continue
			}
			if strings.ToLower(choice.ID) == wanted || strings.ToLower(choice.Name) == wanted {
				found = []agent.ModelChoice{choice}
				break
			}
			if strings.Contains(strings.ToLower(choice.ID), wanted) || strings.Contains(strings.ToLower(choice.Name), wanted) {
				found = append(found, choice)
			}
		}
		if len(found) == 1 {
			ui.selectModel(found[0])
			return
		}
	}
	ui.modelMenu()
}

// showClaudeStatus is /status: the connection's account, model and usage.
func (ui *desktopUI) showClaudeStatus() {
	conn := ui.claudeConnection()
	model := ui.cfg.Model
	if choice, ok := ui.selectedChoice(); ok {
		model = choice.Display()
	}
	if ui.cfg.Effort != "" {
		model += " · " + ui.cfg.Effort + " effort"
	}
	account := textLine("Loading…", 13, colMuted)
	version := textLine("", 13, colMuted)
	usage := "Shown after this connection's next turn. Send /usage for Claude Code's full report."
	if ui.plan != (agent.PlanUsage{}) {
		usage = fmt.Sprintf("%.0f%% of the 5-hour window · %.0f%% of the week", ui.plan.FiveHour*100, ui.plan.Week*100)
	}
	contextLines := strings.Split(ui.contextRing.describe(), "\n") // the plan line is shown below
	contextUse := strings.Join(contextLines[:min(2, len(contextLines))], " · ")
	line := func(label string, value fyne.CanvasObject) fyne.CanvasObject {
		return container.NewBorder(nil, nil, container.NewGridWrap(fyne.NewSize(110, 20), textLine(label, 13, colFaint)), nil, value)
	}
	body := container.New(layout.NewCustomPaddedVBoxLayout(6),
		line("Connection", textLine(conn.Name, 13, colText)),
		line("Account", account),
		line("Model", textLine(model, 13, colText)),
		line("Context", plainFlow(contextUse, 13, colText)),
		line("Plan usage", plainFlow(usage, 13, colText)),
		line("Claude Code", version))
	ui.modal("Status", body, 520, nil)
	show := func(info agent.ClaudeInfo, err error) {
		if err != nil {
			account.Text = agent.FirstLine(err.Error())
			account.Refresh()
			return
		}
		text := info.Email
		if info.Organization != "" {
			text += " · " + info.Organization
		}
		if info.Subscription != "" {
			text += " · " + info.Subscription
		}
		if text == "" {
			text = "Signed in"
		}
		account.Text, account.Color = text, colText
		account.Refresh()
		version.Text = info.Version
		version.Refresh()
	}
	if info, ok := ui.backend.CachedClaudeInfo(conn, ui.commandDir()); ok {
		show(info, nil)
		return
	}
	ui.loadClaudeInfo(show)
}

// buildCommandMenu makes the menu that offers commands over the composer.
func (ui *desktopUI) buildCommandMenu() fyne.CanvasObject {
	ui.commandList = container.New(layout.NewCustomPaddedVBoxLayout(0))
	background := canvas.NewRectangle(colPopover)
	background.TopLeftCornerRadius, background.TopRightCornerRadius = 12, 12
	background.StrokeColor = colBorderSoft
	background.StrokeWidth = 1
	ui.commandTray = padded(0, 0, 16, 16, container.NewStack(background, padded(4, 6, 4, 4, ui.commandList)))
	ui.commandTray.Hide()
	ui.prompt.keys = ui.commandMenuKey
	return ui.commandTray
}

// commandTyped is the command name being typed, when the composer holds
// only "/" and a name so far.
func (ui *desktopUI) commandTyped() (string, bool) {
	text := ui.prompt.Text
	if !ui.commandsHere() || !strings.HasPrefix(text, "/") || strings.ContainsAny(text, " \t\n") {
		return "", false
	}
	if _, _, ok := agent.SlashCommand(text); !ok && text != "/" {
		return "", false
	}
	return text[1:], true
}

// updateCommandMenu shows the commands matching what is typed.
func (ui *desktopUI) updateCommandMenu() {
	if ui.commandTray == nil {
		return
	}
	typed, ok := ui.commandTyped()
	if !ok || ui.prompt.Text == ui.commandDismissed {
		ui.hideCommandMenu()
		return
	}
	ui.commandDismissed = ""
	info, fresh := ui.backend.CachedClaudeInfo(ui.claudeConnection(), ui.commandDir())
	if !fresh && ui.commandsErr == nil {
		ui.loadClaudeInfo(func(agent.ClaudeInfo, error) { ui.updateCommandMenu() })
	}
	matches := matchCommands(commandOptions(info), typed)
	if len(matches) == 0 && !ui.commandsLoading && ui.commandsErr == nil {
		ui.hideCommandMenu()
		return
	}
	if typed != ui.commandFilter {
		ui.commandPick = 0
	}
	ui.commandFilter, ui.commandMatches = typed, matches
	ui.commandPick = max(0, min(ui.commandPick, len(matches)-1))
	ui.renderCommandMenu()
}

const commandMenuRows = 8

func (ui *desktopUI) renderCommandMenu() {
	ui.commandList.RemoveAll()
	first := max(0, min(ui.commandPick-commandMenuRows/2, len(ui.commandMatches)-commandMenuRows))
	for i := first; i < len(ui.commandMatches) && i < first+commandMenuRows; i++ {
		ui.commandList.Add(ui.commandRow(i))
	}
	if ui.commandsLoading {
		ui.commandList.Add(padded(4, 4, 10, 10, textLine("Loading Claude Code's commands…", 12.5, colFaint)))
	} else if ui.commandsErr != nil {
		ui.commandList.Add(padded(4, 4, 10, 10, textLine("Claude Code did not list its commands: "+agent.FirstLine(ui.commandsErr.Error()), 12.5, colFaint)))
	}
	if !ui.commandTray.Visible() {
		ui.commandTray.Show()
	}
	ui.commandTray.Refresh()
	ui.relayoutColumn()
}

func (ui *desktopUI) commandRow(index int) fyne.CanvasObject {
	option := ui.commandMatches[index]
	fill := color.Color(color.Transparent)
	if index == ui.commandPick {
		fill = colSelected
	}
	highlight := canvas.NewRectangle(fill)
	highlight.CornerRadius = 8
	name := newRow("/"+option.name, nil, styleChip, func() {
		ui.commandPick = index
		ui.completeCommand()
	})
	name.size = 13.5
	left := container.NewHBox(name)
	if option.hint != "" {
		left.Add(container.NewCenter(textLine(option.hint, 12, colFaint)))
	}
	about := newFitLabel(option.about, 12.5, colMuted)
	return container.NewStack(highlight, padded(0, 0, 2, 8, container.NewBorder(nil, nil, left, nil, about)))
}

func (ui *desktopUI) hideCommandMenu() {
	ui.commandMatches = nil
	if ui.commandTray != nil && ui.commandTray.Visible() {
		ui.commandTray.Hide()
		ui.relayoutColumn()
	}
}

// completeCommand puts the chosen command in the composer, ready for its
// arguments.
func (ui *desktopUI) completeCommand() {
	if ui.commandPick >= len(ui.commandMatches) {
		return
	}
	text := "/" + ui.commandMatches[ui.commandPick].name + " "
	ui.prompt.SetText(text)
	ui.prompt.CursorRow, ui.prompt.CursorColumn = 0, len([]rune(text))
	ui.prompt.Refresh()
	ui.hideCommandMenu()
	ui.window.Canvas().Focus(ui.prompt)
}

// commandMenuKey moves in the menu while it is open: Up and Down choose,
// Tab completes, Enter completes a name being typed (or sends it when it is
// whole), Escape closes the menu.
func (ui *desktopUI) commandMenuKey(key *fyne.KeyEvent) bool {
	if len(ui.commandMatches) == 0 {
		return false
	}
	switch key.Name {
	case fyne.KeyUp:
		ui.commandPick = (ui.commandPick + len(ui.commandMatches) - 1) % len(ui.commandMatches)
	case fyne.KeyDown:
		ui.commandPick = (ui.commandPick + 1) % len(ui.commandMatches)
	case fyne.KeyTab:
		ui.completeCommand()
		return true
	case fyne.KeyReturn, fyne.KeyEnter:
		if ui.commandMatches[ui.commandPick].name == ui.commandFilter {
			ui.hideCommandMenu()
			return false // the whole name: Enter sends it
		}
		ui.completeCommand()
		return true
	case fyne.KeyEscape:
		ui.commandDismissed = ui.prompt.Text
		ui.hideCommandMenu()
		return true
	default:
		return false
	}
	ui.renderCommandMenu()
	return true
}
