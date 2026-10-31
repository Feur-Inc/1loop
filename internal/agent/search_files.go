package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"1loop/internal/platform"
)

// searchFiles keeps rg's no-match exit separate from operational failures.
func searchFiles(ctx context.Context, root, raw string) (string, error) {
	var args struct {
		Path, Pattern string
		MaxTokens     *int `json:"max_output_tokens"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", err
	}
	if args.Path == "" {
		args.Path = "."
	}
	machine := machineOf(ctx)
	var target string
	var err error
	if machine != nil {
		target, err = MachinePath(ctx, machine, root, args.Path)
	} else {
		target, err = toolPath(ctx, root, args.Path)
	}
	if err != nil {
		return "", err
	}
	if args.Pattern == "" {
		return "", fmt.Errorf("Provide a nonempty regular expression")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	capture := &commandCapture{}
	argv := []string{"--no-config", "--line-number", "--no-heading", "--color", "never", "--max-count", "100", "--max-filesize", "1M", "-e", args.Pattern, "--", target}
	code := -1
	if machine == nil {
		cmd := exec.CommandContext(ctx, "rg", argv...)
		platform.WorkspaceCommand(cmd, root, platform.WorkspaceYOLO(ctx))
		platform.PrepareProcess(cmd)
		cmd.Cancel = func() error { return platform.KillProcessTree(cmd) }
		cmd.WaitDelay = time.Second
		cmd.Stdout, cmd.Stderr = &capture.stdout, &capture.stderr
		err = cmd.Run()
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
	} else {
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
		words := []string{"rg"}
		for _, arg := range argv {
			words = append(words, quote(arg))
		}
		var wait func() (int, error)
		if streams, ok := machine.(interface {
			StartStreams(context.Context, string, string, io.Writer, io.Writer) (func() (int, error), error)
		}); ok {
			wait, err = streams.StartStreams(ctx, root, strings.Join(words, " "), &capture.stdout, &capture.stderr)
		} else {
			wait, err = machine.Start(ctx, root, strings.Join(words, " "), &capture.stdout)
		}
		if err == nil {
			code, err = wait()
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if code == 1 {
		err = nil
	}
	status := "matches"
	if code == 1 && err == nil {
		status = "no_matches"
	}
	if err != nil {
		status = "failed"
	}
	tokens := 10000
	if args.MaxTokens != nil {
		tokens = min(50000, max(1000, *args.MaxTokens))
	}
	result := map[string]any{"status": status, "path": target, "output": headAndTail(string(capture.stdout.data), tokens), "stderr": headAndTail(string(capture.stderr.data), 1000), "exit_code": code,
		"truncated": capture.stdout.total > len(capture.stdout.data) || len(capture.stdout.data) > tokens*4}
	if err != nil {
		result["error"] = err.Error()
	}
	data, _ := json.Marshal(result)
	return string(data), err
}
