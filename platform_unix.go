//go:build !windows

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func shellDescription() string { return "Linux bash" }
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "bash", "-c", command)
}
func prepareProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
func prepareComputerProcess(command *exec.Cmd) { prepareProcess(command) }
func killProcessTree(command *exec.Cmd) error {
	return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
func interruptProcess(command *exec.Cmd) error { return command.Process.Signal(os.Interrupt) }
func restartProcess(executable string) error {
	return syscall.Exec(executable, os.Args, os.Environ())
}
func cpuTime() time.Duration {
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}
func watchDevSignals() {
	go func() {
		asked := make(chan os.Signal, 1)
		signal.Notify(asked, syscall.SIGUSR1)
		for range asked {
			devStacks("goroutines, on request (SIGUSR1)")
		}
	}()
	go func() {
		asked := make(chan os.Signal, 1)
		signal.Notify(asked, syscall.SIGUSR2)
		for range asked {
			devProfile(10 * time.Second)
		}
	}()
}
func unifiedDiff(ctx context.Context, path, before, after string) (string, error) {
	oldReader, oldWriter, err := os.Pipe()
	if err != nil {
		return "", err
	}
	defer oldReader.Close()
	newReader, newWriter, err := os.Pipe()
	if err != nil {
		oldWriter.Close()
		return "", err
	}
	defer newReader.Close()
	cmd := exec.CommandContext(ctx, "diff", "-u", "--label", "a/"+path, "--label", "b/"+path, "/dev/fd/3", "/dev/fd/4")
	cmd.ExtraFiles = []*os.File{oldReader, newReader}
	go func() { defer oldWriter.Close(); io.WriteString(oldWriter, before) }()
	go func() { defer newWriter.Close(); io.WriteString(newWriter, after) }()
	output, err := cmd.CombinedOutput()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
			return "", fmt.Errorf("Creating diff: %w", err)
		}
	}
	return string(output), nil
}
