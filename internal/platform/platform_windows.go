package platform

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"golang.org/x/sys/windows"
)

func ShellDescription() string { return "Windows PowerShell" }
func ShellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false);"+command)
}
func PrepareProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
func PrepareComputerProcess(command *exec.Cmd) {
	// SW_HIDE also hides the helper's first consent dialog. Suppress only
	// its console, leaving Windows Forms permission dialogs visible.
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP}
}
func KillProcessTree(command *exec.Cmd) error {
	kill := exec.Command("taskkill.exe", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		return command.Process.Kill()
	}
	return nil
}
func InterruptProcess(command *exec.Cmd) error {
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(command.Process.Pid)); err == nil {
		return nil
	}
	return KillProcessTree(command)
}
func RestartProcess(executable string) error {
	command := exec.Command(executable, os.Args[1:]...)
	command.Env = os.Environ()
	if err := command.Start(); err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
func CpuTime() time.Duration {
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user) != nil {
		return 0
	}
	ticks := uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)
	ticks += uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime)
	return time.Duration(ticks) * 100 * time.Nanosecond
}
func watchDevSignals() {}
func UnifiedDiff(ctx context.Context, path, before, after string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(before), B: difflib.SplitLines(after), FromFile: "a/" + path, ToFile: "b/" + path, Context: 3})
}
