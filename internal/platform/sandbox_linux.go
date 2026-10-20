//go:build linux

package platform

// The workspace sandbox for Auto mode on Linux: Landlock (kernel 5.13+),
// enforced by the kernel, so it costs nothing while the command runs. The
// command reads and runs everything, writes only in the workspace, the
// temporary folders and the build caches, and keeps the network.
//
// Landlock restricts the thread that asks and what it starts, so a command
// is started through this program itself: SandboxCommand runs
// `<self> 1loop-sandbox <workspace> <program> <args…>`, and SandboxMain, the
// first thing main does, restricts that process and execs the command.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const sandboxArg = "1loop-sandbox"

// Landlock's system calls (the same numbers on every architecture) and
// filesystem rights.
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446

	landlockCreateRulesetVersion = 1
	landlockRulePathBeneath      = 1

	accessExecute    = 1 << 0
	accessWriteFile  = 1 << 1
	accessReadFile   = 1 << 2
	accessReadDir    = 1 << 3
	accessRemoveDir  = 1 << 4
	accessRemoveFile = 1 << 5
	accessMakeChar   = 1 << 6
	accessMakeDir    = 1 << 7
	accessMakeReg    = 1 << 8
	accessMakeSock   = 1 << 9
	accessMakeFifo   = 1 << 10
	accessMakeBlock  = 1 << 11
	accessMakeSym    = 1 << 12
	accessRefer      = 1 << 13 // ABI 2
	accessTruncate   = 1 << 14 // ABI 3

	accessRead  = accessExecute | accessReadFile | accessReadDir
	accessWrite = accessWriteFile | accessRemoveDir | accessRemoveFile | accessMakeChar | accessMakeDir |
		accessMakeReg | accessMakeSock | accessMakeFifo | accessMakeBlock | accessMakeSym | accessRefer | accessTruncate
)

type landlockRulesetAttr struct{ handledAccessFS uint64 }

// landlockPathBeneathAttr is packed in the kernel: 8 + 4 bytes.
type landlockPathBeneathAttr struct {
	allowedAccess uint64
	parentFD      int32
}

var landlockABI = sync.OnceValue(func() int {
	abi, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return 0
	}
	return int(abi)
})

// SandboxAvailable says whether Auto mode can run commands in the
// workspace sandbox: Landlock here, or the phone's own project launcher.
func SandboxAvailable() bool {
	if launcher := os.Getenv("ONELOOP_PROJECT_LAUNCHER"); launcher != "" {
		return true
	}
	return landlockABI() >= 1
}

// SandboxCommand makes command run in the workspace sandbox. On the phone
// the project launcher already confines every command that is not YOLO.
func SandboxCommand(command *exec.Cmd, workspace string) error {
	if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	command.Args = append([]string{self, sandboxArg, workspace, command.Path}, command.Args[1:]...)
	command.Path = self
	return nil
}

// SandboxMain restricts this process and execs the command when it was
// started by SandboxCommand; otherwise it returns at once.
func SandboxMain() {
	if len(os.Args) < 4 || os.Args[1] != sandboxArg {
		return
	}
	workspace, program, args := os.Args[2], os.Args[3], os.Args[3:]
	if err := restrictToWorkspace(workspace); err != nil {
		fmt.Fprintln(os.Stderr, "1loop sandbox:", err)
		os.Exit(126)
	}
	err := syscall.Exec(program, args, os.Environ())
	fmt.Fprintln(os.Stderr, "1loop sandbox:", err)
	os.Exit(127)
}

// sandboxWritable are the folders a command may change besides the
// workspace: temporary files and the caches build tools fill.
func sandboxWritable(workspace string) []string {
	paths := []string{workspace, os.TempDir(), "/tmp", "/var/tmp", "/dev/shm"}
	for _, name := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "XDG_CACHE_HOME", "CARGO_HOME", "npm_config_cache"} {
		if value := os.Getenv(name); value != "" {
			paths = append(paths, value)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range []string{".cache", "go", ".npm", ".cargo", ".rustup", ".gradle", ".m2", ".bun", ".yarn", ".pnpm-store", ".local/share/pnpm", ".nuget"} {
			paths = append(paths, filepath.Join(home, name))
		}
	}
	return paths
}

func restrictToWorkspace(workspace string) error {
	abi := landlockABI()
	if abi < 1 {
		return fmt.Errorf("Landlock is not available")
	}
	handled := uint64(accessRead | accessWrite)
	if abi < 2 {
		handled &^= accessRefer
	}
	if abi < 3 {
		handled &^= accessTruncate
	}
	attr := landlockRulesetAttr{handledAccessFS: handled}
	fd, _, errno := syscall.Syscall(sysLandlockCreateRuleset, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %v", errno)
	}
	ruleset := int(fd)
	defer syscall.Close(ruleset)
	allow := func(dir string, access uint64) error {
		file, err := os.Open(dir)
		if err != nil {
			return err // a cache that does not exist yet is skipped by the caller
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if !info.IsDir() {
			// Directory-only rights do not apply to a file.
			access &= accessExecute | accessWriteFile | accessReadFile | accessTruncate
		}
		rule := landlockPathBeneathAttr{allowedAccess: access & handled, parentFD: int32(file.Fd())}
		_, _, errno := syscall.Syscall6(sysLandlockAddRule, uintptr(ruleset), landlockRulePathBeneath, uintptr(unsafe.Pointer(&rule)), 0, 0, 0)
		if errno != 0 {
			return fmt.Errorf("landlock_add_rule %s: %v", dir, errno)
		}
		return nil
	}
	if err := allow("/", accessRead); err != nil {
		return err
	}
	// Terminals and /dev/null are written to; nothing is made or removed there.
	if err := allow("/dev", accessRead|accessWriteFile|accessTruncate); err != nil {
		return err
	}
	if err := allow(workspace, accessRead|accessWrite); err != nil {
		return err
	}
	for _, dir := range sandboxWritable(workspace)[1:] {
		allow(dir, accessRead|accessWrite)
	}
	// The restriction applies to this thread, which then execs.
	runtime.LockOSThread()
	// No set-uid program may gain rights back (sudo included).
	if _, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, 38 /* PR_SET_NO_NEW_PRIVS */, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %v", errno)
	}
	if _, _, errno := syscall.Syscall(sysLandlockRestrictSelf, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %v", errno)
	}
	return nil
}
