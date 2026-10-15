//go:build !windows

package main

import (
	"context"
	"os"
	"os/exec"

	"github.com/creack/pty"
)

type unixTerminal struct {
	*os.File
	command *exec.Cmd
}

func newTerminalProcess(ctx context.Context, dir string) (terminalProcess, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	command := exec.CommandContext(ctx, shell, "-i")
	command.Dir = dir
	command.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	file, err := pty.StartWithSize(command, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		return nil, err
	}
	return &unixTerminal{File: file, command: command}, nil
}
func (terminal *unixTerminal) resize(columns, rows int) error {
	return pty.Setsize(terminal.File, &pty.Winsize{Rows: uint16(rows), Cols: uint16(columns)})
}
func (terminal *unixTerminal) wait() int {
	terminal.command.Wait()
	if terminal.command.ProcessState == nil {
		return -1
	}
	return terminal.command.ProcessState.ExitCode()
}
