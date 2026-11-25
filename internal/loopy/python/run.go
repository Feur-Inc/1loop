// SPDX-License-Identifier: MPL-2.0

//go:build !js

package python

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed boot.py
var boot []byte

//go:embed autolink.py
var sdk []byte

// Limits of a run.
const (
	Time    = 2 * time.Minute
	MaxTime = 30 * time.Minute
	Printed = 64 << 10
	Given   = 1 << 20
	line    = 32 << 20
)

// Job is a script to run.
type Job struct {
	Dir   string // the AutoLink's: main.py, lib
	Data  string // kept between runs
	Out   string // this run's files
	Input json.RawMessage
	Sites []string // the only ones it reaches; "*" for any
	Test  bool
	Limit time.Duration
	// Call answers what the script asks of 1loop.
	Call func(ctx context.Context, name string, args json.RawMessage) (any, error)
}

// Result is what a run did.
type Result struct {
	Printed string
	Outputs []json.RawMessage
	Files   []string // absolute, inside Out or Data
	Error   string
	Took    time.Duration
}

// environment is all a script sees of the computer's: what the network and
// the system need, nothing of the user's.
func environment(home string) []string {
	env := []string{"HOME=" + home, "TMPDIR=" + home, "TMP=" + home, "TEMP=" + home, "USERPROFILE=" + home, "LANG=C.UTF-8",
		"PYTHONUTF8=1", "PYTHONDONTWRITEBYTECODE=1", "PIP_CONFIG_FILE=" + os.DevNull, "PIP_NO_INPUT=1"}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy", "TZ", "SystemRoot", "SYSTEMROOT", "windir", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "LD_LIBRARY_PATH"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

// Requirement is the form of a library a script may ask for: a name of
// PyPI's and a version, never an address, a file or an option.
var Requirement = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9,._-]+\])?\s*((===?|~=|!=|>=|<=|>|<)\s*[A-Za-z0-9.*+!_-]+(\s*,\s*(===?|~=|!=|>=|<=|>|<)\s*[A-Za-z0-9.*+!_-]+)*)?$`)

// Install puts the libraries in dir/lib, once for a list. Only built
// packages: none runs code of its own while it is installed.
func (p *Python) Install(ctx context.Context, dir string, requirements []string, progress func(string)) error {
	if err := p.ensure(ctx, progress); err != nil {
		return err
	}
	for _, requirement := range requirements {
		if !Requirement.MatchString(requirement) {
			return fmt.Errorf("%q is not a library's name and version, such as \"requests\" or \"requests>=2.32\"", requirement)
		}
	}
	lib := filepath.Join(dir, "lib")
	mark := filepath.Join(dir, "lib.txt")
	want := strings.Join(slices.Sorted(slices.Values(requirements)), "\n")
	if had, err := os.ReadFile(mark); err == nil && string(had) == want && fileExists(lib) {
		return nil
	}
	if len(requirements) == 0 {
		os.RemoveAll(lib)
		if err := os.MkdirAll(lib, 0700); err != nil {
			return err
		}
		return os.WriteFile(mark, []byte(want), 0600)
	}
	if progress != nil {
		progress("Installing " + strings.Join(requirements, ", "))
	}
	fresh, err := os.MkdirTemp(dir, ".lib-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(fresh)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	args := append([]string{"-I", "-m", "pip", "install", "--quiet", "--disable-pip-version-check", "--no-input", "--no-cache-dir", "--no-compile", "--only-binary", ":all:", "--target", fresh}, requirements...)
	cmd := exec.CommandContext(ctx, p.exe, args...)
	alone(cmd)
	cmd.Env = environment(fresh)
	if p.ca != "" {
		cmd.Env = append(cmd.Env, "SSL_CERT_FILE="+p.ca)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if len(text) > 1500 {
			text = text[len(text)-1500:]
		}
		return fmt.Errorf("these libraries could not be installed (only built packages are): %s", text)
	}
	os.RemoveAll(lib)
	if err := os.Rename(fresh, lib); err != nil {
		return err
	}
	return os.WriteFile(mark, []byte(want), 0600)
}

// Prepare gets the interpreter ready, downloading it the first time;
// progress is told how far.
func (p *Python) Prepare(ctx context.Context, progress func(string)) error {
	return p.ensure(ctx, progress)
}

// Check compiles a script without running it.
func (p *Python) Check(ctx context.Context, code string) error {
	if err := p.ensure(ctx, nil); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.exe, "-I", "-c", "import sys, traceback\ntry: compile(sys.stdin.buffer.read().decode('utf-8'), 'main.py', 'exec')\nexcept Exception as e: print(''.join(traceback.format_exception_only(e))); sys.exit(1)")
	hidden(cmd)
	cmd.Stdin = strings.NewReader(code)
	cmd.Env = environment(os.TempDir())
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}

// Run runs main.py of the job.
func (p *Python) Run(ctx context.Context, job Job) (Result, error) {
	if err := p.ensure(ctx, nil); err != nil {
		return Result{}, err
	}
	limit := job.Limit
	if limit <= 0 {
		limit = Time
	}
	limit = min(limit, MaxTime)
	for _, dir := range []string{job.Data, job.Out, filepath.Join(job.Dir, "lib")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return Result{}, err
		}
	}
	// The boot, out of the script's reach to change.
	home, err := os.MkdirTemp("", "1loop-autolink-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(home)
	for name, content := range map[string][]byte{"boot.py": boot, "autolink.py": sdk} {
		if err := os.WriteFile(filepath.Join(home, name), content, 0600); err != nil {
			return Result{}, err
		}
	}
	input := job.Input
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	sites := job.Sites
	if sites == nil {
		sites = []string{}
	}
	first, err := json.Marshal(map[string]any{"input": input, "dir": job.Dir, "lib": filepath.Join(job.Dir, "lib"), "data": job.Data, "out": job.Out, "main": filepath.Join(job.Dir, "main.py"),
		"boot": filepath.Join(home, "boot.py"), "sites": sites, "test": job.Test, "ca": p.ca})
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.exe, "-I", "-B", "-X", "utf8", filepath.Join(home, "boot.py"))
	alone(cmd)
	cmd.Dir = job.Data
	cmd.Env = environment(job.Out)
	if p.ca != "" {
		cmd.Env = append(cmd.Env, "SSL_CERT_FILE="+p.ca, "REQUESTS_CA_BUNDLE="+p.ca)
	}
	cmd.WaitDelay = 3 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Result{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, err
	}
	errs := &tail{limit: 8 << 10}
	cmd.Stderr = errs
	begun := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	go stdin.Write(append(first, '\n'))
	var result Result
	var printed bytes.Buffer
	ended, cut, given := false, false, 0
	reader := bufio.NewReaderSize(stdout, 64<<10)
	for {
		raw, err := readLine(reader)
		if err != nil {
			break
		}
		var message struct {
			Print  *string
			Output json.RawMessage
			File   string
			Call   string
			Args   json.RawMessage
			End    *bool
			Error  string
		}
		if json.Unmarshal(raw, &message) != nil {
			continue
		}
		switch {
		case message.Print != nil:
			if room := Printed - printed.Len(); room > 0 {
				printed.WriteString((*message.Print)[:min(room, len(*message.Print))])
			} else {
				cut = true
			}
		case message.Call != "":
			answer := map[string]any{}
			value, err := job.Call(ctx, message.Call, message.Args)
			if err != nil {
				answer["error"] = err.Error()
			} else {
				answer["result"] = value
			}
			data, err := json.Marshal(answer)
			if err != nil {
				data = []byte(`{"error":"1loop's answer could not be written"}`)
			}
			if _, err := stdin.Write(append(data, '\n')); err != nil && ctx.Err() == nil {
				result.Error = "it stopped listening"
			}
		case message.File != "":
			if file := within(message.File, job.Out, job.Data); file != "" && len(result.Files) < 50 {
				result.Files = append(result.Files, file)
			} else {
				printed.WriteString("\n(" + message.File + " is not a file of autolink.out or autolink.data)\n")
			}
		case message.Output != nil:
			if given += len(message.Output); given <= Given {
				result.Outputs = append(result.Outputs, message.Output)
			} else {
				cut = true
			}
		case message.End != nil:
			ended = true
			if !*message.End {
				result.Error = strings.TrimSpace(message.Error)
			}
		}
	}
	stdin.Close()
	waited := cmd.Wait()
	result.Took = time.Since(begun)
	result.Printed = printed.String()
	if text := strings.TrimSpace(errs.String()); text != "" {
		result.Printed = strings.TrimRight(result.Printed, "\n") + "\n" + text
	}
	if cut {
		result.Printed += "\n(the rest is not kept)"
	}
	switch {
	case ended:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Error = fmt.Sprintf("stopped after %s", limit)
	case ctx.Err() != nil:
		result.Error = "stopped"
	case waited != nil:
		result.Error = "it ended by itself: " + waited.Error()
	default:
		result.Error = "it ended by itself"
	}
	return result, nil
}

// within is a real file's path when it is in one of the folders.
func within(path string, dirs ...string) string {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	for _, dir := range dirs {
		dir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue
		}
		if relative, err := filepath.Rel(dir, path); err == nil && filepath.IsLocal(relative) {
			return path
		}
	}
	return ""
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		part, err := reader.ReadSlice('\n')
		out = append(out, part...)
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
		if len(out) > line {
			return nil, errors.New("a line too long")
		}
	}
}

// tail keeps the end of what is written.
type tail struct {
	mu    sync.Mutex
	b     []byte
	limit int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.limit {
		t.b = append([]byte(nil), t.b[len(t.b)-t.limit:]...)
	}
	return len(p), nil
}

func (t *tail) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }

var _ io.Writer = (*tail)(nil)

// Where says where it runs, for the settings' page.
func (p *Python) Where() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exe == "" {
		return ""
	}
	if strings.HasPrefix(p.exe, p.dir) {
		return "Python " + version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
	}
	return p.exe
}
