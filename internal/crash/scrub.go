// SPDX-License-Identifier: MPL-2.0

package crash

// The scrubber: what makes a report safe to post on a public issue. It
// keeps the report's shape (which code, which line, what kind of error)
// and drops what says who or what it was about: names, folders, files,
// addresses, secrets, and anything the user or a model wrote.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Scrubber knows the few things about this computer that must never be
// shown, beyond what any text can be recognized as.
type Scrubber struct {
	Home string // the user's home folder, shown as ~
	// Names are the login name and the computer's name, never shown.
	Names []string
	// Module is 1loop's source folder when the binary was built from one
	// (not -trimpath); its paths are shown relative to it. Found in the
	// trace itself when empty.
	Module string
}

// NewScrubber is the scrubber for this computer.
func NewScrubber() Scrubber {
	var s Scrubber
	s.Home, _ = os.UserHomeDir()
	if s.Home != "" {
		s.Names = append(s.Names, filepath.Base(s.Home))
	}
	for _, name := range []string{"USER", "USERNAME", "LOGNAME"} {
		s.Names = append(s.Names, os.Getenv(name))
	}
	if host, err := os.Hostname(); err == nil {
		s.Names = append(s.Names, host, strings.SplitN(host, ".", 2)[0])
	}
	return s
}

var (
	urlPattern   = regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"'<>()\[\]{}]+`)
	emailPattern = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+\b`)
	// Secrets: a known prefix, a name = value, a bearer or a JWT.
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\b(sk-ant-|sk-proj-|sk-|ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[abprs]-|AIza|hf_|npm_)[A-Za-z0-9_\-]{8,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}(\.[A-Za-z0-9_-]+)?`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`),
	}
	bearer      = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
	namedSecret = regexp.MustCompile(`(?i)\b((?:api[_-]?)?key|token|secret|password|passwd|pwd|authorization|cookie|session|bearer|code)(\s*[=:]\s*|\s+)("[^"]*"|'[^']*'|[^\s,;&)]+)`)
	// A long run of letters and digits mixed: a key, a hash, an identifier.
	longToken = regexp.MustCompile(`\b[A-Za-z0-9_+/=-]{24,}\b`)
	// Identifiers of chats, devices and requests: UUIDs and long hex.
	idPattern     = regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b|\b[0-9a-f]{16,}\b`)
	quoted        = regexp.MustCompile("\"(?:[^\"\\\\\n]|\\\\.)*\"|`[^`\n]*`|“[^”\n]*”|«[^»\n]*»")
	windowsPath   = regexp.MustCompile(`\b[A-Za-z]:\\[^\s"'<>|]*`)
	absolutePath  = regexp.MustCompile(`(^|[\s(\[=,:'])(~?/[^\s"'<>()\[\],;]*|~)`)
	relativePath  = regexp.MustCompile(`(^|[\s(\[=,:'])((?:[\w.@-]+/)+[\w@-]+\.[A-Za-z0-9]{1,5})\b`)
	fileName      = regexp.MustCompile(`(?i)(^|[\s(\[=,:'])([\w-]+\.(?:go|py|js|mjs|ts|tsx|jsx|kt|kts|java|rs|c|h|cc|cpp|hpp|cs|rb|php|swift|lua|sh|zsh|bash|ps1|md|txt|rst|json|jsonl|yaml|yml|toml|ini|cfg|conf|env|lock|xml|html|htm|css|scss|svg|png|jpe?g|gif|webp|pdf|docx?|xlsx?|pptx?|csv|log|sql|db|sqlite|zip|tar|gz|tgz|mp3|mp4|mov|wav|apk|deb|exe|dmg|key|pem|crt))\b`)
	ipv4          = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b`)
	ipv6          = regexp.MustCompile(`(?i)\[?\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b\]?|\[?(?:[0-9a-f]{1,4}:)*[0-9a-f]{0,4}::(?:[0-9a-f]{1,4}:)*[0-9a-f]{1,4}\b\]?`)
	ipv6Letters   = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b`)
	hostPattern   = regexp.MustCompile(`\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:com|net|org|io|dev|app|ai|co|me|fr|de|uk|eu|us|ca|nl|be|ch|es|it|jp|cn|ru|br|in|xyz|cloud|sh|local|lan|home|internal|arpa|onion)(?::\d{1,5})?\b`)
	namedThing    = regexp.MustCompile(`(?i)\b(chat|title|prompt|project|workspace|machine|host|user|folder|repository|repo|branch|model output|tool args|args|arguments)(=|: | )([^\s<>,;)]+)`)
	safeNamedWord = map[string]bool{"is": true, "was": true, "has": true, "in": true, "of": true, "to": true, "and": true, "or": true, "not": true, "the": true, "a": true, "an": true, "for": true, "on": true, "with": true, "from": true, "ready": true, "list": true, "screen": true, "opened": true, "closed": true, "saved": true}
)

// ownFolders are 1loop's own folders under the home: they are kept.
var ownFolders = []string{"~/.config/1loop", "~/.cache/1loop", "~/.local/share/1loop", "~/.config/1loop-server"}

// systemFolders hold the system's own files (libraries, the runtime):
// nothing about the user, and what a native crash is about.
var systemFolders = []string{"/usr/", "/lib/", "/lib64/", "/lib32/", "/bin/", "/sbin/", "/etc/", "/proc/", "/sys/", "/dev/", "/nix/store/", "/app/", "/snap/", "/system/", "/apex/", "/vendor/", "/data/app/"}

// Text scrubs any text: a panic's message, an exception's, a log line.
func (s Scrubber) Text(text string) string {
	text = strings.ReplaceAll(text, "\r", "")
	text = urlPattern.ReplaceAllString(text, "<url>")
	text = emailPattern.ReplaceAllString(text, "<email>")
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllString(text, "<secret>")
	}
	text = bearer.ReplaceAllString(text, "<secret>")
	text = namedSecret.ReplaceAllStringFunc(text, func(match string) string {
		parts := namedSecret.FindStringSubmatch(match)
		value := parts[3]
		// "key: 3" or "token count" are not secrets; "code 2" neither.
		spaced := strings.TrimSpace(parts[2]) == ""
		if spaced && !strings.EqualFold(parts[1], "bearer") || strings.HasPrefix(value, "<") || isNumber(value) {
			return match
		}
		return parts[1] + parts[2] + "<secret>"
	})
	text = quoted.ReplaceAllStringFunc(text, func(match string) string {
		open, close := []rune(match)[0], []rune(match)[len([]rune(match))-1]
		return string(open) + "…" + string(close)
	})
	text = s.paths(text)
	text = idPattern.ReplaceAllString(text, "<id>")
	text = longToken.ReplaceAllStringFunc(text, func(match string) string {
		if hasLetterAndDigit(match) {
			return "<secret>"
		}
		return match
	})
	text = ipv4.ReplaceAllStringFunc(text, func(match string) string {
		host, _, _ := strings.Cut(match, ":")
		for _, part := range strings.Split(host, ".") {
			if len(part) > 1 && part[0] == '0' || len(part) == 3 && part > "255" {
				return match // a version number, not an address
			}
		}
		return "<ip>"
	})
	text = ipv6.ReplaceAllString(text, "<ip>")
	text = ipv6Letters.ReplaceAllStringFunc(text, func(match string) string {
		if strings.ContainsAny(strings.ToLower(match), "abcdef") {
			return "<ip>"
		}
		return match // a time of day, 15:04:05
	})
	text = hostPattern.ReplaceAllString(text, "<host>")
	text = s.names(text)
	text = namedThing.ReplaceAllStringFunc(text, func(match string) string {
		parts := namedThing.FindStringSubmatch(match)
		value := parts[3]
		spaced := map[string]bool{"project": true, "workspace": true, "machine": true, "host": true, "folder": true, "repository": true, "repo": true, "branch": true}
		if parts[2] == " " && !spaced[strings.ToLower(parts[1])] {
			return match // "open chat took 120ms"
		}
		if strings.ContainsAny(value[:1], "<~\"'`“«") || safeNamedWord[strings.ToLower(value)] || isNumber(value) {
			return match
		}
		return parts[1] + parts[2] + "<name>"
	})
	return text
}

// paths shortens 1loop's paths, keeps the system's and hides the rest.
func (s Scrubber) paths(text string) string {
	home := filepath.ToSlash(strings.TrimRight(s.Home, `/\`))
	module := filepath.ToSlash(strings.TrimRight(s.Module, `/\`))
	text = windowsPath.ReplaceAllStringFunc(text, func(match string) string {
		return s.path(filepath.ToSlash(strings.ReplaceAll(match, `\`, "/")), home, module)
	})
	text = absolutePath.ReplaceAllStringFunc(text, func(match string) string {
		parts := absolutePath.FindStringSubmatch(match)
		path := parts[2]
		trail := len(path) - len(strings.TrimRight(path, ":.,"))
		return parts[1] + s.path(path[:len(path)-trail], home, module) + path[len(path)-trail:]
	})
	text = relativePath.ReplaceAllStringFunc(text, func(match string) string {
		parts := relativePath.FindStringSubmatch(match)
		if isModulePath(parts[2]) || strings.Contains(parts[2], "@v") {
			return match
		}
		return parts[1] + "<path>"
	})
	return fileName.ReplaceAllStringFunc(text, func(match string) string {
		parts := fileName.FindStringSubmatch(match)
		switch strings.ToLower(parts[2]) {
		case "state.json", "dev.log", "crash.log", "hooks.json", "settings.json", "main.go":
			return match
		}
		return parts[1] + "<file>"
	})
}

// path is one absolute path as the report shows it.
func (s Scrubber) path(path, home, module string) string {
	if path == "~" || path == "/" {
		return path
	}
	if module != "" && (path == module || strings.HasPrefix(path, module+"/")) {
		if rel := strings.TrimPrefix(strings.TrimPrefix(path, module), "/"); rel != "" {
			return rel
		}
		return "<1loop>"
	}
	if home != "" && (path == home || strings.HasPrefix(path, home+"/")) {
		path = "~" + strings.TrimPrefix(path, home)
	}
	if strings.HasPrefix(path, "~") {
		for _, own := range ownFolders {
			if path == own || strings.HasPrefix(path, own+"/") {
				return path
			}
		}
		return "~/<path>"
	}
	for _, folder := range systemFolders {
		if strings.HasPrefix(path, folder) && !strings.HasPrefix(path, "/home/") {
			return path
		}
	}
	return "<path>"
}

// names hides the user's and the computer's names wherever they are.
func (s Scrubber) names(text string) string {
	seen := map[string]bool{}
	names := append([]string(nil), s.Names...)
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		name = strings.TrimSpace(name)
		if len(name) < 3 || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		pattern := regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])` + regexp.QuoteMeta(name) + `($|[^A-Za-z0-9])`)
		for pattern.MatchString(text) {
			text = pattern.ReplaceAllString(text, "${1}<name>${2}")
		}
	}
	return text
}

// isModulePath says whether a relative path is in 1loop's own source.
func isModulePath(path string) bool {
	for _, top := range []string{"internal/", "cmd/", "third_party/", "android/", "1loop/", "io/oneloop/"} {
		if strings.HasPrefix(path, top) {
			return true
		}
	}
	return false
}

func isNumber(text string) bool {
	if text == "" {
		return false
	}
	if hex, ok := strings.CutPrefix(text, "0x"); ok && hex != "" && strings.Trim(hex, "0123456789abcdefABCDEF") == "" {
		return true
	}
	for _, r := range text {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

func hasLetterAndDigit(text string) bool {
	return strings.ContainsAny(text, "0123456789") && strings.IndexFunc(text, func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	}) >= 0
}

var (
	goroutineLine = regexp.MustCompile(`^goroutine \d+( gp=\S+ m=\S+( mp=\S+)?)? \[[^\]]*\]:$`)
	fileLine      = regexp.MustCompile(`^\t(.+):(\d+)(?: \+0x[0-9a-f]+)?(?: fp=0x[0-9a-f]+ sp=0x[0-9a-f]+ pc=0x[0-9a-f]+)?$`)
	createdBy     = regexp.MustCompile(`^created by (\S+)( in goroutine \d+)?$`)
	registerLine  = regexp.MustCompile(`^(r[a-z0-9]{1,3}|e[a-z]{2}|[re]?ip|[re]?flags|cs|fs|gs|pc|lr|sp|x\d+|fault|trap|err|oldmask|mask)\s+0x[0-9a-f]+`)
)

// Trace scrubs a Go trace: the panic's message as text, then the frames of
// 1loop and its dependencies with paths relative to their module. The
// runtime's own frames and the call arguments are left out.
func (s Scrubber) Trace(trace string) string {
	lines := strings.Split(strings.ReplaceAll(trace, "\r", ""), "\n")
	if s.Module == "" {
		s.Module = moduleRoot(lines)
	}
	var out []string
	skipFile := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case goroutineLine.MatchString(line):
			out = append(out, line)
			skipFile = false
		case fileLine.MatchString(line):
			if skipFile {
				skipFile = false
				continue
			}
			match := fileLine.FindStringSubmatch(line)
			function := ""
			if i > 0 {
				function = frameFunction(lines[i-1])
				if created := createdBy.FindStringSubmatch(lines[i-1]); created != nil {
					function = created[1]
				}
			}
			out = append(out, "\t"+s.framePath(function, match[1])+":"+match[2])
		case registerLine.MatchString(strings.TrimSpace(line)):
			// Registers say nothing a reader can use.
		case createdBy.MatchString(line):
			match := createdBy.FindStringSubmatch(line)
			if strings.HasPrefix(match[1], "runtime.") {
				skipFile = true // the runtime's own goroutines
				continue
			}
			out = append(out, "created by "+match[1]+match[2])
		case i+1 < len(lines) && fileLine.MatchString(lines[i+1]):
			function := frameFunction(line)
			if function == "panic" || strings.HasPrefix(function, "runtime.") || strings.HasPrefix(function, "runtime/") {
				skipFile = true
				continue
			}
			out = append(out, function+"(...)")
		default:
			out = append(out, s.Text(line))
		}
	}
	return strings.TrimSpace(strings.Join(dropEmptyGoroutines(out), "\n"))
}

// dropEmptyGoroutines leaves out the goroutines none of whose frames were
// kept: the runtime's.
func dropEmptyGoroutines(lines []string) []string {
	var kept []string
	for i, line := range lines {
		if goroutineLine.MatchString(line) && (i+1 == len(lines) || strings.TrimSpace(lines[i+1]) == "" || goroutineLine.MatchString(lines[i+1])) {
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
				lines[i+1] = "\x00" // no double blank line
			}
			continue
		}
		if line != "\x00" {
			kept = append(kept, line)
		}
	}
	return kept
}

// frameFunction is a frame line's function, without its arguments.
func frameFunction(line string) string {
	line = strings.TrimSpace(line)
	if open := strings.LastIndex(line, "("); open > 0 && strings.HasSuffix(line, ")") {
		line = line[:open]
	}
	return line
}

// packageOf is a function's package path: 1loop/internal/ui for
// 1loop/internal/ui.(*desktopUI).render.func1.
func packageOf(function string) string {
	slash := strings.LastIndex(function, "/")
	dot := strings.Index(function[slash+1:], ".")
	if dot < 0 {
		return function
	}
	return function[:slash+1+dot]
}

// moduleRoot finds 1loop's source folder from a frame of its own: the
// file of 1loop/internal/ui.f is <root>/internal/ui/<file>.go.
func moduleRoot(lines []string) string {
	for i := 1; i < len(lines); i++ {
		match := fileLine.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		file := filepath.ToSlash(strings.ReplaceAll(match[1], `\`, "/"))
		function := frameFunction(lines[i-1])
		if created := createdBy.FindStringSubmatch(lines[i-1]); created != nil {
			function = created[1]
		}
		if pkg := packageOf(function); strings.HasPrefix(pkg, "1loop/") {
			dir := strings.TrimPrefix(pkg, "1loop")
			if index := strings.LastIndex(file, dir+"/"); index > 0 {
				return file[:index]
			}
		} else if strings.HasPrefix(function, "main.") {
			if index := strings.LastIndex(file, "/cmd/"); index > 0 {
				return file[:index]
			}
		}
	}
	return ""
}

// framePath is a frame's file as the report shows it.
func (s Scrubber) framePath(function, path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	module := filepath.ToSlash(strings.TrimRight(s.Module, "/"))
	switch {
	case module != "" && strings.HasPrefix(path, module+"/"):
		return strings.TrimPrefix(path, module+"/")
	case strings.Contains(path, "/pkg/mod/"):
		return path[strings.LastIndex(path, "/pkg/mod/")+len("/pkg/mod/"):]
	case !strings.HasPrefix(path, "/") && !windowsPath.MatchString(path):
		return strings.TrimPrefix(path, "1loop/") // built with -trimpath
	}
	if pkg := packageOf(function); pkg != "" && !strings.Contains(strings.SplitN(pkg, "/", 2)[0], ".") {
		// The standard library: from GOROOT's src.
		if index := strings.LastIndex(path, "/src/"+pkg+"/"); index >= 0 {
			return "go" + path[index:]
		}
		if index := strings.LastIndex(path, "/src/runtime/"); index >= 0 {
			return "go" + path[index:] // linked into the runtime (internal/poll.runtime_pollWait)
		}
	}
	return "<path>"
}

// Log scrubs the lines of the developer log. Areas that carry what was
// said or done (the run, tools, hooks, the model's API) keep only their
// time and area; the others are scrubbed as text.
func (s Scrubber) Log(lines []string) []string {
	logLine := regexp.MustCompile(`^(\d\d:\d\d:\d\d\.\d{3}) (\S+)\s+(.*)$`)
	private := map[string]bool{"run": true, "tool": true, "hooks": true, "agy": true, "agent": true, "api": true, "approval": true, "claude": true, "remote": true, "computer": true, "codex": true, "mcp": true, "github": true, "ssh": true}
	var out, stack []string
	flush := func() {
		if len(stack) > 0 {
			out = append(out, s.Trace(strings.Join(stack, "\n")))
			stack = nil
		}
	}
	for _, line := range lines {
		match := logLine.FindStringSubmatch(line)
		if match == nil {
			// A continuation (a stack the watchdog dumped): kept as a trace.
			if strings.TrimSpace(line) != "" {
				stack = append(stack, line)
			}
			continue
		}
		flush()
		switch {
		case private[match[2]]:
			out = append(out, match[1]+" "+match[2]+" <omitted>")
		default:
			out = append(out, match[1]+" "+match[2]+" "+s.Text(match[3]))
		}
	}
	flush()
	return out
}
