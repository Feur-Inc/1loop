package agent

import (
	"bufio"
	"context"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"time"

	"1loop/internal/platform"
)

// ClaudeSignIn follows Claude Code's own login; credentials stay with the CLI.
type ClaudeSignIn struct {
	Input io.WriteCloser
	Done  <-chan error
}

func StartClaudeSignIn(ctx context.Context, dir, browser string, onURL func(string)) (*ClaudeSignIn, error) {
	path, err := ClaudeExecutable()
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, path, "auth", "login", "--claudeai")
	command.Dir = dir
	command.Env = os.Environ()
	if browser != "" {
		command.Env = append(command.Env, "BROWSER="+browser)
	}
	platform.PrepareProcess(command)
	command.WaitDelay = 3 * time.Second
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
