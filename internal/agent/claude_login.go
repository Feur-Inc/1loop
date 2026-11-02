package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"1loop/internal/platform"
)

// ClaudeSignIn follows Claude Code's own login; credentials stay with the CLI.
type ClaudeSignIn struct {
	Input io.WriteCloser
	Done  <-chan error
}

// claudeAuthCommand runs claude auth in the connection's Claude Code folder:
// a connection with its own login signs in and out there only.
func claudeAuthCommand(ctx context.Context, conn Connection, args ...string) (*exec.Cmd, error) {
	path, err := ClaudeExecutable()
	if err != nil {
		return nil, err
	}
	home := conn.ClaudeHome()
	command := exec.CommandContext(ctx, path, append([]string{"auth"}, args...)...)
	command.Env = os.Environ()
	if home != "" {
		if err := prepareClaudeHome(home); err != nil {
			return nil, err
		}
		command.Env = nil
		for _, pair := range os.Environ() {
			if name, _, _ := strings.Cut(pair, "="); name != "CLAUDE_CONFIG_DIR" && name != "CLAUDE_CODE_OAUTH_TOKEN" {
				command.Env = append(command.Env, pair)
			}
		}
		command.Env = append(command.Env, "CLAUDE_CONFIG_DIR="+home)
	}
	platform.PrepareProcess(command)
	command.WaitDelay = 3 * time.Second
	return command, nil
}

// SignOutClaude signs a connection with its own login out of Claude Code
// (its sessions stay in its folder, for chat import). The system login is
// the user's and stays signed in.
func SignOutClaude(ctx context.Context, conn Connection) error {
	if conn.ClaudeHome() == "" {
		return nil
	}
	if _, err := os.Stat(conn.ClaudeHome()); err != nil {
		return nil // never used
	}
	command, err := claudeAuthCommand(ctx, conn, "logout")
	if err != nil {
		return err
	}
	command.Dir = os.TempDir()
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("Claude Code could not sign out: %s", FirstLine(strings.TrimSpace(string(output))+" "+err.Error()))
	}
	return nil
}

// StartClaudeSignIn signs the connection in to Claude Code.
func StartClaudeSignIn(ctx context.Context, conn Connection, dir, browser string, onURL func(string)) (*ClaudeSignIn, error) {
	command, err := claudeAuthCommand(ctx, conn, "login", "--claudeai")
	if err != nil {
		return nil, err
	}
	command.Dir = dir
	if browser != "" {
		command.Env = append(command.Env, "BROWSER="+browser)
	}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	command.Stderr = command.Stdout
	command.Cancel = func() error { return input.Close() }
	if err := command.Start(); err != nil {
		input.Close()
		output.Close()
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		defer input.Close()
		links := regexp.MustCompile(`https://[^\s\x1b]+`)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			for _, candidate := range links.FindAllString(scanner.Text(), -1) {
				parsed, err := url.Parse(candidate)
				if err == nil && claudeAuthorizationURL(parsed) {
					onURL(candidate)
				}
			}
		}
		scanErr := scanner.Err()
		err := command.Wait()
		if err == nil {
			err = scanErr
		}
		done <- err
		close(done)
	}()
	return &ClaudeSignIn{Input: input, Done: done}, nil
}

func claudeAuthorizationURL(link *url.URL) bool {
	if link.Scheme != "https" || link.User != nil || link.Port() != "" {
		return false
	}
	if link.Hostname() == "claude.com" {
		return link.Path == "/cai/oauth/authorize"
	}
	return link.Path == "/oauth/authorize" && (link.Hostname() == "claude.ai" || link.Hostname() == "platform.claude.com" || link.Hostname() == "console.anthropic.com")
}
