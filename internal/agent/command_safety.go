package agent

// How Auto mode sorts a shell command before running it, as Codex does
// (shell-command/src/command_safety): a few programs that only read run at
// once; the commands that destroy (rm, git reset, sudo…) always go to the
// reviewer; everything else runs in the workspace sandbox without a review
// (platform.SandboxAvailable), or goes to the reviewer where there is none.
// The command is parsed as bash: anything the parser cannot read with
// certainty is not safe.

import (
	"context"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"1loop/internal/platform"
)

type commandClass int

const (
	commandSafe commandClass = iota
	commandOther
	commandDangerous
)

func (class commandClass) String() string {
	return [...]string{"safe", "other", "dangerous"}[class]
}

// classifyCommand is the most severe class among every program the command
// may start, including in pipes, lists, substitutions and `bash -c` scripts.
func classifyCommand(command string) commandClass {
	if runtime.GOOS == "windows" {
		return classifyPowerShell(command)
	}
	return classifyScript(command, 0)
}

func classifyScript(script string, depth int) commandClass {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil || depth > 3 {
		return worst(commandOther, dangerousWords(strings.Fields(script)))
	}
	class := commandSafe
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Stmt:
			for _, redirect := range node.Redirs {
				// Writing a file, or reading a here-document into a shell, is not a read.
				if redirect.Op != syntax.DplOut && redirect.Op != syntax.DplIn && redirect.Op != syntax.RdrIn && !(redirect.Word != nil && literal(redirect.Word) == "/dev/null") {
					class = worst(class, commandOther)
				}
			}
			if node.Background || node.Coprocess {
				class = worst(class, commandOther)
			}
		case *syntax.CallExpr:
			class = worst(class, classifyCall(node, depth))
		case *syntax.BinaryCmd:
			// Code piped into a shell is unknown code: `curl … | sh`.
			if node.Op == syntax.Pipe || node.Op == syntax.PipeAll {
				if call, ok := node.Y.Cmd.(*syntax.CallExpr); ok && len(call.Args) > 0 && isShell(literal(call.Args[0])) && len(call.Args) == 1 {
					class = worst(class, commandDangerous)
				}
			}
		case *syntax.FuncDecl, *syntax.ArithmCmd, *syntax.CoprocClause, *syntax.LetClause, *syntax.DeclClause, *syntax.TestClause:
			class = worst(class, commandOther)
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ParamExp, *syntax.ArithmExp, *syntax.ExtGlob:
			class = worst(class, commandOther)
		}
		return true
	})
	return class
}

func worst(a, b commandClass) commandClass {
	if b > a {
		return b
	}
	return a
}

// literal is a word's text when it is only literal parts and quotes, "" otherwise.
func literal(word *syntax.Word) string {
	if word == nil {
		return ""
	}
	value, ok := literalParts(word.Parts)
	if !ok {
		return ""
	}
	return value
}

func literalParts(parts []syntax.WordPart) (string, bool) {
	var out strings.Builder
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.Lit:
			out.WriteString(part.Value)
		case *syntax.SglQuoted:
			out.WriteString(part.Value)
		case *syntax.DblQuoted:
			inner, ok := literalParts(part.Parts)
			if !ok {
				return "", false
			}
			out.WriteString(inner)
		default:
			return "", false
		}
	}
	return out.String(), true
}

func classifyCall(call *syntax.CallExpr, depth int) commandClass {
	if len(call.Args) == 0 {
		return commandOther // a bare assignment
	}
	words := make([]string, len(call.Args))
	allLiteral := true
	for i, arg := range call.Args {
		words[i] = literal(arg)
		if words[i] == "" && len(arg.Parts) > 0 {
			allLiteral = false
			// An unreadable word may hide anything, but the program's
			// own name still tells a destructive command.
			if i == 0 {
				return commandOther
			}
		}
	}
	class := classifyWords(words, depth)
	if class == commandSafe && (!allLiteral || len(call.Assigns) > 0) {
		return commandOther
	}
	return class
}

// classifyWords sorts one program and its arguments.
func classifyWords(words []string, depth int) commandClass {
	if len(words) == 0 {
		return commandOther
	}
	if class := dangerousWords(words); class == commandDangerous {
		return class
	}
	program := path.Base(words[0])
	args := words[1:]
	switch program {
	// Wrappers: what they start decides.
	case "env":
		for len(args) > 0 && (strings.Contains(args[0], "=") || strings.HasPrefix(args[0], "-")) {
			args = args[1:]
		}
		return worst(commandOther, classifyWords(args, depth))
	case "nice", "nohup", "time", "command", "stdbuf", "ionice", "exec":
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			args = args[1:]
		}
		return worst(commandOther, classifyWords(args, depth))
	case "timeout":
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			args = args[1:]
		}
		if len(args) > 0 {
			args = args[1:] // the duration
		}
		return worst(commandOther, classifyWords(args, depth))
	case "xargs":
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			args = args[1:]
		}
		if len(args) == 0 {
			return commandOther // xargs runs echo
		}
		return worst(commandOther, classifyWords(args, depth))
	case "bash", "sh", "zsh", "dash":
		for i, arg := range args {
			if arg == "-c" || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.HasSuffix(arg, "c")) {
				if i+1 < len(args) {
					return worst(commandOther, classifyScript(args[i+1], depth+1))
				}
			}
		}
		return commandOther
	}
	if safeCommand(program, args) {
		return commandSafe
	}
	return commandOther
}

func isShell(word string) bool {
	switch path.Base(word) {
	case "sh", "bash", "zsh", "dash":
		return true
	}
	return false
}

// dangerousWords tells the commands that destroy or reach beyond the
// workspace's owner: they always go to the reviewer, in or out of the
// sandbox.
func dangerousWords(words []string) commandClass {
	if len(words) == 0 {
		return commandSafe
	}
	program := path.Base(words[0])
	args := words[1:]
	has := func(options ...string) bool {
		for _, arg := range args {
			for _, option := range options {
				if arg == option || (len(option) == 2 && option[0] == '-' && option[1] != '-' && strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsRune(arg[1:], rune(option[1]))) {
					return true
				}
			}
		}
		return false
	}
	switch program {
	case "rm", "rmdir", "unlink", "shred", "srm", "wipe", "wipefs", "truncate", "dd", "fdisk", "sfdisk", "parted",
		"sudo", "doas", "su", "pkexec", "kill", "killall", "pkill", "shutdown", "reboot", "poweroff", "halt":
		return commandDangerous
	case "chmod", "chown", "chgrp":
		if has("-R", "--recursive") {
			return commandDangerous
		}
	case "find":
		if has("-delete") {
			return commandDangerous
		}
		// find … -exec rm … ;
		for i, arg := range args {
			if (arg == "-exec" || arg == "-execdir" || arg == "-ok" || arg == "-okdir") && i+1 < len(args) {
				if dangerousWords(args[i+1:]) == commandDangerous {
					return commandDangerous
				}
			}
		}
	case "systemctl":
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			switch args[0] {
			case "status", "show", "list-units", "list-unit-files", "is-active", "is-enabled", "cat":
			default:
				return commandDangerous
			}
		}
	case "crontab":
		if has("-r") {
			return commandDangerous
		}
	case "git":
		return dangerousGit(args)
	}
	if strings.HasPrefix(program, "mkfs") {
		return commandDangerous
	}
	return commandSafe
}

// dangerousGit: the git commands that throw work away or rewrite what others
// see. Global options (-C dir, -c key=value) come before the subcommand.
func dangerousGit(args []string) commandClass {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if (args[0] == "-C" || args[0] == "-c") && len(args) > 1 {
			args = args[1:]
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return commandSafe
	}
	sub, rest := args[0], args[1:]
	has := func(options ...string) bool {
		for _, arg := range rest {
			for _, option := range options {
				if arg == option || strings.HasPrefix(arg, option+"=") {
					return true
				}
			}
		}
		return false
	}
	switch sub {
	case "reset", "rm", "clean", "restore", "filter-branch", "filter-repo":
		return commandDangerous
	case "checkout":
		if has("--", "-f", "--force", ".", "-p", "--patch") {
			return commandDangerous
		}
	case "switch":
		if has("-f", "--force", "--discard-changes") {
			return commandDangerous
		}
	case "push":
		// Publishing leaves the computer: always reviewed, forced or not.
		return commandDangerous
	case "branch":
		if has("-D", "-d", "--delete", "-M", "--force") {
			return commandDangerous
		}
	case "stash":
		if len(rest) > 0 && (rest[0] == "drop" || rest[0] == "clear") {
			return commandDangerous
		}
	case "reflog":
		if len(rest) > 0 && (rest[0] == "expire" || rest[0] == "delete") {
			return commandDangerous
		}
	case "update-ref":
		if has("-d") {
			return commandDangerous
		}
	case "worktree":
		if len(rest) > 0 && rest[0] == "remove" {
			return commandDangerous
		}
	case "gc":
		if has("--prune") {
			return commandDangerous
		}
	}
	return commandSafe
}

// safeCommand is Codex's list of programs that only read, with the options
// that would write a file or start another program left out. Git only reads
// here when the repository's configuration cannot start a program
// (fsmonitor, pager, external diff, textconv), so only its plainest reads.
func safeCommand(program string, args []string) bool {
	has := func(test func(string) bool) bool {
		for _, arg := range args {
			if test(arg) {
				return true
			}
		}
		return false
	}
	switch program {
	case "cat", "cd", "cut", "echo", "expr", "false", "grep", "head", "id", "ls", "nl", "paste", "pwd", "rev",
		"seq", "stat", "tail", "tr", "true", "uname", "uniq", "wc", "which", "whoami", "numfmt", "tac",
		"basename", "dirname", "realpath", "file", "du", "df", "printf", "test", "[":
		return true
	case "sort", "tree":
		return !has(func(arg string) bool { return arg == "-o" || strings.HasPrefix(arg, "--output") })
	case "date":
		return !has(func(arg string) bool { return arg == "-s" || strings.HasPrefix(arg, "--set") })
	case "base64":
		return !has(func(arg string) bool { return arg == "-o" || strings.HasPrefix(arg, "--output") })
	case "find":
		return !has(func(arg string) bool {
			switch arg {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fls", "-fprint", "-fprint0", "-fprintf":
				return true
			}
			return false
		})
	case "rg":
		return !has(func(arg string) bool {
			return arg == "--pre" || strings.HasPrefix(arg, "--pre=") || strings.HasPrefix(arg, "--hostname-bin") ||
				arg == "--search-zip" || arg == "-z" || (strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "z"))
		})
	case "sed":
		// Only `sed -n Np` or `sed -n M,Np`, with files: GNU sed's e and w commands run programs and write files.
		if len(args) < 2 || args[0] != "-n" {
			return false
		}
		return sedPrint(args[1]) && !has(func(arg string) bool { return strings.HasPrefix(arg, "-") && arg != "-n" })
	case "git":
		if len(args) == 0 {
			return false
		}
		switch args[0] {
		case "status", "rev-parse", "ls-files":
		case "branch":
			for _, arg := range args[1:] {
				if arg != "--list" && arg != "-a" && arg != "-r" && arg != "-v" && arg != "-vv" && arg != "--show-current" {
					return false
				}
			}
		default:
			return false
		}
		return true
	}
	return false
}

func sedPrint(script string) bool {
	body, ok := strings.CutSuffix(script, "p")
	if !ok || body == "" {
		return false
	}
	first, second, comma := strings.Cut(body, ",")
	digits := func(text string) bool {
		if text == "" {
			return false
		}
		for _, r := range text {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return digits(first) && (!comma || digits(second))
}

// classifyPowerShell: Windows has no sandbox and no PowerShell parser here,
// so only a single plain read is safe; the deletion cmdlets and their
// aliases are dangerous, everything else goes to the reviewer.
func classifyPowerShell(command string) commandClass {
	if strings.ContainsAny(command, ";|&`$(){}<>\n") {
		for _, part := range strings.FieldsFunc(command, func(r rune) bool { return strings.ContainsRune(";|&\n", r) }) {
			if powerShellDangerous(strings.Fields(part)) {
				return commandDangerous
			}
		}
		return commandOther
	}
	words := strings.Fields(command)
	if powerShellDangerous(words) {
		return commandDangerous
	}
	if len(words) > 0 {
		switch strings.ToLower(words[0]) {
		case "get-childitem", "gci", "ls", "dir", "get-content", "gc", "cat", "type", "get-location", "pwd",
			"select-string", "sls", "get-item", "gi", "test-path", "resolve-path", "measure-object", "echo", "write-output":
			for _, word := range words[1:] {
				if strings.HasPrefix(strings.ToLower(word), "-outfile") {
					return commandOther
				}
			}
			return commandSafe
		}
	}
	return commandOther
}

func powerShellDangerous(words []string) bool {
	if len(words) == 0 {
		return false
	}
	switch strings.ToLower(words[0]) {
	case "remove-item", "ri", "rm", "rmdir", "rd", "del", "erase", "clear-content", "clc", "clear-item", "cli",
		"format-volume", "stop-process", "kill", "spps", "stop-computer", "restart-computer", "remove-itemproperty",
		"set-executionpolicy", "start-process":
		return true
	case "git":
		return dangerousGit(words[1:]) == commandDangerous
	}
	return false
}

// shellApproval decides how a shell command runs. Outside Auto mode the
// approval policy decides alone (the user, or YOLO). In Auto mode a plain
// read runs at once; a dangerous command, or one asked to leave the
// sandbox, goes to the reviewer; anything else runs in the workspace
// sandbox without a review, or, where there is no sandbox, goes to the
// reviewer unless it only creates in the workspace.
func shellApproval(ctx context.Context, root, command string, escalate bool, reason string) (review, sandboxed bool, title, preview string, err error) {
	title, preview = "Run command?", root+"\n\n$ "+command
	// On an SSH machine there is no sandbox: what is not plainly safe is reviewed.
	machine := machineOf(ctx)
	if machine != nil {
		preview = machine.Label() + ": " + preview
	}
	policy, _ := ctx.Value(approvalContextKey{}).(approvalPolicy)
	if policy.review == nil {
		return true, false, title, preview, nil
	}
	class := classifyCommand(command)
	switch {
	case escalate && machine == nil:
		if os.Getenv("ONELOOP_PROJECT_LAUNCHER") != "" {
			return false, false, "", "", fmt.Errorf("On the phone, commands always stay in the project outside YOLO mode")
		}
		if strings.TrimSpace(reason) == "" {
			return false, false, "", "", fmt.Errorf("Give a justification to run a command outside the sandbox")
		}
		return true, false, "Run outside the sandbox?", preview + "\n\nWhy: " + strings.TrimSpace(reason), nil
	case class == commandDangerous:
		return true, false, title, preview, nil
	case class == commandSafe:
		return false, false, title, preview, nil
	case machine == nil && platform.SandboxAvailable():
		return false, true, title, preview, nil
	case machine == nil && runtime.GOOS != "windows" && createsOnly(root, command):
		return false, false, title, preview, nil
	}
	return true, false, title, preview, nil
}

// createsOnly is a command made only of mkdir, touch, cp and mv inside the
// workspace that overwrite nothing (see createsInWorkspace), joined by &&.
func createsOnly(root, command string) bool {
	if strings.ContainsAny(command, "`$<>|;\n&") && !strings.Contains(command, "&&") {
		return false
	}
	for _, part := range strings.Split(command, "&&") {
		words := strings.Fields(part)
		if len(words) == 0 || strings.ContainsAny(part, "`$<>|;\n&") || createOptions[words[0]] == nil || !createsInWorkspace(root, words) {
			return false
		}
	}
	return true
}

// sandboxRefusal tells an error from the sandbox in a failed command's output.
func sandboxRefusal(output string) bool {
	lower := strings.ToLower(output)
	for _, sign := range []string{"permission denied", "permission non accordée", "read-only file system", "operation not permitted", "opération non permise", "système de fichiers accessible en lecture seulement"} {
		if strings.Contains(lower, sign) {
			return true
		}
	}
	return false
}
