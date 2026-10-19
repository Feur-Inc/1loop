//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"

	"1loop/internal/platform"

	"github.com/creack/pty"
)

type unixTerminal struct {
	*os.File
	command *exec.Cmd
}

func NewTerminalProcess(ctx context.Context, dir string, hosts ...NativeHost) (Process, error) {
	if len(hosts) > 0 && hosts[0] != nil {
		return newNativeTerminal(ctx, dir, hosts[0])
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	command := exec.CommandContext(ctx, shell, "-i")
	platform.WorkspaceCommand(command, dir, platform.WorkspaceYOLO(ctx))
	command.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	file, err := pty.StartWithSize(command, &pty.Winsize{Rows: 30, Cols: 100})
	if err != nil {
		return nil, err
	}
	return &unixTerminal{File: file, command: command}, nil
}

type nativeTerminal struct {
	*os.File
	host      NativeHost
	id        string
	ctx       context.Context
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func newNativeTerminal(ctx context.Context, dir string, host NativeHost) (Process, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, err
	}
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		master.Close()
		return nil, err
	}
	id, err := host.Start(ctx, dir, slave.Name(), platform.WorkspaceYOLO(ctx))
	if err != nil {
		master.Close()
		return nil, err
	}
	process := &nativeTerminal{File: master, host: host, id: id, ctx: ctx, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			process.Close()
		case <-process.done:
		}
	}()
	return process, nil
}

func (process *nativeTerminal) Resize(columns, rows int) error {
	return pty.Setsize(process.File, &pty.Winsize{Rows: uint16(rows), Cols: uint16(columns)})
}
func (process *nativeTerminal) Wait() int {
	defer close(process.done)
	code, err := process.host.Wait(process.ctx, process.id)
	if err != nil {
		return -1
	}
	return code
}
func (process *nativeTerminal) Close() error {
	process.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		process.closeErr = process.host.Close(ctx, process.id)
		process.File.Close()
	})
	return process.closeErr
}
func (terminal *unixTerminal) Resize(columns, rows int) error {
	return pty.Setsize(terminal.File, &pty.Winsize{Rows: uint16(rows), Cols: uint16(columns)})
}
func (terminal *unixTerminal) Wait() int {
	terminal.command.Wait()
	if terminal.command.ProcessState == nil {
		return -1
	}
	return terminal.command.ProcessState.ExitCode()
}
