package agent

// Slash commands in a Claude Code conversation. Claude Code runs in its
// stream mode (claudecode.go), where only some of its commands work: its
// initialize handshake lists them, with the user's and the project's
// .claude/commands, skills and plugins. 1loop sends those as they are and
// handles a few interactive ones itself (internal/ui/slash_commands.go).

import (
	"context"
	"os"
	"strings"
	"time"
)

// ClaudeCommand is a command Claude Code accepts in its stream mode.
type ClaudeCommand struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argumentHint"`
	Aliases      []string `json:"aliases"`
}

// ClaudeInfo is what Claude Code reports about a connection in a folder.
type ClaudeInfo struct {
	Commands     []ClaudeCommand
	Email        string
	Organization string
	Subscription string // "Claude Pro", "Claude Max", …
	Version      string
	loaded       time.Time
}

// claudeInfoFresh is how long a listing serves before it is asked again:
// commands and skills added on disk show up after that.
const claudeInfoFresh = 5 * time.Minute

// Command finds a command by its name or one of its aliases.
func (info ClaudeInfo) Command(name string) (ClaudeCommand, bool) {
	for _, command := range info.Commands {
		if command.Name == name || Contains(command.Aliases, name) {
			return command, true
		}
	}
	return ClaudeCommand{}, false
}

func claudeInfoKey(conn Connection, dir string) string { return conn.ID + "\x00" + dir }

// CachedClaudeInfo is the last listing for conn in dir, without starting
// Claude Code; ok is false when there is none or it is stale.
func (backend *App) CachedClaudeInfo(conn Connection, dir string) (info ClaudeInfo, ok bool) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	info, ok = backend.claudeInfo[claudeInfoKey(conn, dir)]
	return info, ok && time.Since(info.loaded) < claudeInfoFresh
}

// ClaudeInfo asks Claude Code, started for conn in dir (the project, so
// its own commands count), which commands it accepts and who is signed in.
// Only the handshake is exchanged: no request reaches a model.
func (backend *App) ClaudeInfo(ctx context.Context, conn Connection, dir string) (ClaudeInfo, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	extensionArgs, cleanExtensions := claudeExtensionArgs()
	defer cleanExtensions()
	answer, err := backend.claudeHandshake(ctx, dir, extensionArgs...)
	if err != nil {
		return ClaudeInfo{}, err
	}
	info := ClaudeInfo{Commands: answer.Commands, Version: answer.Version, loaded: time.Now()}
	if answer.Account != nil {
		info.Email, info.Organization, info.Subscription = answer.Account.Email, answer.Account.Organization, answer.Account.Subscription
	}
	backend.mu.Lock()
	if backend.claudeInfo == nil {
		backend.claudeInfo = map[string]ClaudeInfo{}
	}
	backend.claudeInfo[claudeInfoKey(conn, dir)] = info
	backend.mu.Unlock()
	return info, nil
}

// SlashCommand splits a prompt that is a slash command, "/compact keep the
// API design", into its name and arguments. A path such as /etc/hosts, or
// text that only starts with a slash, is not one.
func SlashCommand(text string) (name, args string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	name, args, _ = strings.Cut(text[1:], " ")
	if before, after, found := strings.Cut(name, "\n"); found {
		name, args = before, strings.TrimSpace(after+" "+args)
	}
	if name == "" {
		return "", "", false
	}
	for i, r := range name {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !letter && (i == 0 || !strings.ContainsRune("-_:.", r)) {
			return "", "", false
		}
	}
	return name, strings.TrimSpace(args), true
}
