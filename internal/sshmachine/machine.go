package sshmachine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"1loop/internal/terminal"
)

// Machine is one SSH machine: connections opened on first use, and again
// after the network dropped them.
type Machine struct {
	mu       sync.Mutex
	settings Settings
	secrets  Secrets
	trust    TrustFunc
	conns    []*connection
	home     string // the user's home folder there, for ~/
}

// maxConnections bounds the connections to one machine. OpenSSH allows 10
// sessions on each (MaxSessions): terminals, commands and background tasks
// beyond that go on another connection.
const maxConnections = 6

// New is a machine for these settings; nothing connects yet.
func New(settings Settings, secrets Secrets, trust TrustFunc) *Machine {
	return &Machine{settings: settings, secrets: secrets, trust: trust}
}

func (machine *Machine) Label() string { return machine.settings.Label() }

// Settings is what the machine was made with.
func (machine *Machine) Settings() Settings { return machine.settings }

// Close ends every connection.
func (machine *Machine) Close() {
	machine.mu.Lock()
	defer machine.mu.Unlock()
	for _, conn := range machine.conns {
		conn.close()
	}
	machine.conns = nil
}

// connect opens a new connection, with its SFTP client.
func (machine *Machine) connect(ctx context.Context) (*connection, error) {
	client, err := dial(ctx, machine.settings, machine.secrets, machine.trust)
	if err != nil {
		return nil, err
	}
	files, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("%s has no SFTP service, which 1loop needs for files: %w", machine.Label(), err)
	}
	conn := &connection{client: client, files: files, broken: make(chan struct{})}
	go func() {
		client.Wait()
		close(conn.broken)
	}()
	go conn.keepAlive()
	if machine.home == "" {
		if home, err := files.Getwd(); err == nil {
			machine.home = home
		}
	}
	return conn, nil
}

// first is a working connection: the first one alive, or a new one.
func (machine *Machine) first(ctx context.Context) (*connection, error) {
	machine.mu.Lock()
	defer machine.mu.Unlock()
	kept := machine.conns[:0]
	for _, conn := range machine.conns {
		if conn.alive() {
			kept = append(kept, conn)
		} else {
			conn.close()
		}
	}
	machine.conns = kept
	if len(machine.conns) > 0 {
		return machine.conns[0], nil
	}
	conn, err := machine.connect(ctx)
	if err != nil {
		return nil, err
	}
	machine.conns = append(machine.conns, conn)
	return conn, nil
}

// session opens a session on a connection that still has room for one.
func (machine *Machine) session(ctx context.Context) (*ssh.Session, error) {
	if _, err := machine.first(ctx); err != nil {
		return nil, err
	}
	machine.mu.Lock()
	defer machine.mu.Unlock()
	var last error
	for _, conn := range machine.conns {
		if !conn.alive() {
			continue
		}
		session, err := conn.client.NewSession()
		if err == nil {
			return session, nil
		}
		last = err
	}
	if len(machine.conns) >= maxConnections {
		return nil, fmt.Errorf("%s accepts no more sessions: close a terminal or stop a background task (%v)", machine.Label(), last)
	}
	conn, err := machine.connect(ctx)
	if err != nil {
		return nil, err
	}
	machine.conns = append(machine.conns, conn)
	return conn.client.NewSession()
}

func (machine *Machine) files(ctx context.Context) (*sftp.Client, error) {
	conn, err := machine.first(ctx)
	if err != nil {
		return nil, err
	}
	return conn.files, nil
}

// Check connects, and says whether the projects folder exists there.
func (machine *Machine) Check(ctx context.Context) (string, error) {
	files, err := machine.files(ctx)
	if err != nil {
		return "", err
	}
	folder := machine.Path(machine.settings.Projects)
	info, err := files.Stat(folder)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "Connected. The projects folder " + folder + " does not exist yet: 1loop creates it with the first project.", nil
	case err != nil:
		return "", err
	case !info.IsDir():
		return "", fmt.Errorf("%s is not a folder", folder)
	}
	return "Connected. Projects go in " + folder + ".", nil
}

// Path reads ~/ as the user's home folder on the machine.
func (machine *Machine) Path(name string) string {
	machine.mu.Lock()
	home := machine.home
	machine.mu.Unlock()
	if home != "" && (name == "~" || strings.HasPrefix(name, "~/")) {
		return path.Join(home, strings.TrimPrefix(name, "~"))
	}
	return name
}

// ProjectsFolder is where projects go, ~/ resolved (connected first).
func (machine *Machine) ProjectsFolder(ctx context.Context) (string, error) {
	if _, err := machine.files(ctx); err != nil {
		return "", err
	}
	return path.Clean(machine.Path(machine.settings.Projects)), nil
}

// ---------------------------------------------------------------------------
// Files, over SFTP.

func (machine *Machine) ReadFile(ctx context.Context, name string) ([]byte, error) {
	files, err := machine.files(ctx)
	if err != nil {
		return nil, err
	}
	file, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, 64<<20))
}

// ReadPrefix is the start of a file, at most limit bytes.
func (machine *Machine) ReadPrefix(ctx context.Context, name string, limit int64) ([]byte, error) {
	files, err := machine.files(ctx)
	if err != nil {
		return nil, err
	}
	file, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}

// WriteFile replaces a file's content, or creates it when create (and
// fails then if it exists). An existing file keeps its permissions.
func (machine *Machine) WriteFile(ctx context.Context, name string, data []byte, create bool) error {
	files, err := machine.files(ctx)
	if err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_TRUNC
	if create {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	file, err := files.OpenFile(name, flags)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (machine *Machine) Remove(ctx context.Context, name string) error {
	files, err := machine.files(ctx)
	if err != nil {
		return err
	}
	return files.Remove(name)
}

func (machine *Machine) MkdirAll(ctx context.Context, name string) error {
	files, err := machine.files(ctx)
	if err != nil {
		return err
	}
	return files.MkdirAll(name)
}

func (machine *Machine) ReadDir(ctx context.Context, name string) ([]fs.FileInfo, error) {
	files, err := machine.files(ctx)
	if err != nil {
		return nil, err
	}
	return files.ReadDir(name)
}

func (machine *Machine) Lstat(ctx context.Context, name string) (fs.FileInfo, error) {
	files, err := machine.files(ctx)
	if err != nil {
		return nil, err
	}
	return files.Lstat(name)
}

// RealPath is an existing path with its links resolved, by the machine.
func (machine *Machine) RealPath(ctx context.Context, name string) (string, error) {
	files, err := machine.files(ctx)
	if err != nil {
		return "", err
	}
	if _, err := files.Stat(name); err != nil {
		return "", err
	}
	return files.RealPath(name)
}

// ---------------------------------------------------------------------------
// Commands.

// quote is a word for a POSIX shell.
func quote(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// commandScript runs command in dir with bash (else sh), in its own process
// group (setsid, else the shell's job control), its input empty. The
// session's input stays open while 1loop waits: when 1loop closes it (Stop,
// a time limit), the watcher ends the whole group, as closing a session
// alone leaves the command running. The watcher reads that input from fd 3:
// a background list without job control would get /dev/null instead.
func commandScript(dir, command string) string {
	return "exec 3<&0\n" +
		"cd " + quote(dir) + " || exit 1\n" +
		"export PAGER=cat GIT_PAGER=cat GIT_TERMINAL_PROMPT=0 TERM=dumb\n" +
		"shell=sh; command -v bash >/dev/null 2>&1 && shell=bash\n" +
		"if command -v setsid >/dev/null 2>&1; then setsid $shell -c " + quote(command) + " </dev/null &\n" +
		"else set -m 2>/dev/null; $shell -c " + quote(command) + " </dev/null & fi\n" +
		"pid=$!\n" +
		"set +m 2>/dev/null\n" +
		"(while read -r line; do :; done; kill -TERM -$pid 2>/dev/null || kill -TERM $pid 2>/dev/null; sleep 2; kill -KILL -$pid 2>/dev/null || kill -KILL $pid 2>/dev/null) <&3 >/dev/null 2>&1 &\n" +
		"watcher=$!\n" +
		"exec 3<&-\n" +
		"wait $pid\n" +
		"status=$?\n" +
		"kill $watcher 2>/dev/null\n" +
		"wait $watcher 2>/dev/null\n" +
		"exit $status\n"
}

// Start runs a command in dir; output receives stdout and stderr. Cancelling
// ctx stops the command and what it started. wait returns its exit code.
func (machine *Machine) Start(ctx context.Context, dir, command string, output io.Writer) (func() (int, error), error) {
	session, err := machine.session(ctx)
	if err != nil {
		return nil, err
	}
	input, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	session.Stdout, session.Stderr = output, output
	if err := session.Start("sh -c " + quote(commandScript(dir, command))); err != nil {
		session.Close()
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() {
		input.Close()
		// A machine that does not end the group still gets its session closed.
		time.AfterFunc(5*time.Second, func() { session.Close() })
	})
	return func() (int, error) {
		err := session.Wait()
		stop()
		session.Close()
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		var exit *ssh.ExitError
		if errors.As(err, &exit) {
			return exit.ExitStatus(), fmt.Errorf("exit status %d", exit.ExitStatus())
		}
		if err != nil {
			return -1, err
		}
		return 0, nil
	}, nil
}

// Output runs a command and returns its output, for short ones.
func (machine *Machine) Output(ctx context.Context, dir, command string) (string, int, error) {
	var buffer strings.Builder
	var mu sync.Mutex
	wait, err := machine.Start(ctx, dir, command, writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buffer.Write(p)
	}))
	if err != nil {
		return "", -1, err
	}
	code, err := wait()
	mu.Lock()
	defer mu.Unlock()
	return buffer.String(), code, err
}

type writerFunc func([]byte) (int, error)

func (write writerFunc) Write(p []byte) (int, error) { return write(p) }

// ---------------------------------------------------------------------------
// Terminal: the user's login shell in a pseudo-terminal of the machine.

type sshTerminal struct {
	session *ssh.Session
	input   io.WriteCloser
	output  io.Reader
	once    sync.Once
}

func (machine *Machine) Terminal(ctx context.Context, dir string) (terminal.Process, error) {
	session, err := machine.session(ctx)
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 38400, ssh.TTY_OP_OSPEED: 38400}
	if err := session.RequestPty("xterm-256color", 30, 100, modes); err != nil {
		session.Close()
		return nil, err
	}
	input, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	output, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	session.Stderr = io.Discard // a pseudo-terminal sends both on one stream
	shell := `cd ` + quote(dir) + ` 2>/dev/null; export COLORTERM=truecolor; exec "${SHELL:-/bin/sh}" -l`
	if err := session.Start("sh -c " + quote(shell)); err != nil {
		session.Close()
		return nil, err
	}
	process := &sshTerminal{session: session, input: input, output: output}
	context.AfterFunc(ctx, func() { process.Close() })
	return process, nil
}

func (process *sshTerminal) Read(p []byte) (int, error)  { return process.output.Read(p) }
func (process *sshTerminal) Write(p []byte) (int, error) { return process.input.Write(p) }

func (process *sshTerminal) Close() error {
	process.once.Do(func() {
		process.input.Close()
		process.session.Close()
	})
	return nil
}

func (process *sshTerminal) Resize(columns, rows int) error {
	return process.session.WindowChange(rows, columns)
}

func (process *sshTerminal) Wait() int {
	err := process.session.Wait()
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return exit.ExitStatus()
	}
	if err != nil {
		return -1
	}
	return 0
}
