package terminal

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/ActiveState/termtest/conpty"
	"golang.org/x/sys/windows"

	"1loop/internal/platform"
)

type windowsTerminal struct {
	console *conpty.ConPty
	process *os.Process
	done    chan struct{}
	once    sync.Once
	exit    int
}

func NewTerminalProcess(ctx context.Context, dir string) (Process, error) {
	path, err := exec.LookPath("powershell.exe")
	if err != nil {
		return nil, err
	}
	console, err := conpty.New(100, 30)
	if err != nil {
		return nil, err
	}
	pid, handle, err := console.Spawn(path, []string{path, "-NoLogo", "-NoProfile"}, &syscall.ProcAttr{Dir: dir, Env: os.Environ()})
	if err != nil {
		console.Close()
		return nil, err
	}
	windows.CloseHandle(windows.Handle(handle))
	process, err := os.FindProcess(pid)
	if err != nil {
		console.Close()
		return nil, err
	}
	terminal := &windowsTerminal{console: console, process: process, done: make(chan struct{}), exit: -1}
	go func() {
		state, _ := process.Wait()
		if state != nil {
			terminal.exit = state.ExitCode()
		}
		close(terminal.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			terminal.Close()
		case <-terminal.done:
		}
	}()
	return terminal, nil
}
func (terminal *windowsTerminal) Read(data []byte) (int, error) {
	return terminal.console.OutPipe().Read(data)
}
func (terminal *windowsTerminal) Write(data []byte) (int, error) {
	return terminal.console.InPipe().Write(data)
}
func (terminal *windowsTerminal) Resize(columns, rows int) error {
	return terminal.console.Resize(uint16(columns), uint16(rows))
}
func (terminal *windowsTerminal) Wait() int { <-terminal.done; return terminal.exit }
func (terminal *windowsTerminal) Close() error {
	var err error
	terminal.once.Do(func() {
		select {
		case <-terminal.done:
		default:
			platform.KillProcessTree(&exec.Cmd{Process: terminal.process})
		}
		err = terminal.console.Close()
	})
	return err
}
