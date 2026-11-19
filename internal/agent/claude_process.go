// SPDX-License-Identifier: MPL-2.0

package agent

// A chat's Claude Code process is kept between its turns: the next prompt
// goes to the same process instead of a new one resuming the session from
// its file (a few seconds per turn, and the session read again). It is
// kept only while nothing that shapes it changes (model, effort, approval
// mode, 1loop's instructions, extensions…), for claudeIdleLimit at most.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"1loop/internal/extensions"
	"1loop/internal/platform"
)

// claudeProcessKey is what shapes a process: its arguments (model, effort,
// approval mode, 1loop's instructions…), its folder and login, its
// compaction window and the user's extensions. The tools 1loop's MCP server
// lists are asked again at each turn instead (mcp_reconnect).
func claudeProcessKey(cfg Config, args []string) string {
	active, _ := json.Marshal(extensions.Enabled())
	return strings.Join(args, "\x00") + "\x00" + cfg.ClaudeHome + "\x00" + cfg.Workspace + "\x00" + cfg.MachineID +
		fmt.Sprintf("\x00%d\x00%v\x00", cfg.CompactAt, cfg.Todo != nil) + string(active)
}

// claudeIdleLimit is how long a kept process waits for the chat's next turn.
var claudeIdleLimit = 10 * time.Minute

type claudeProcess struct {
	key     string // what shaped it: the same key reuses it
	session string // Claude Code's session id, from its init event
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stderr  *outputBuffer
	binding *todoMCPBinding // nil without 1loop's MCP server
	lines   chan []byte     // its output, line by line; closed when it exits
	quit    chan struct{}   // closed when the process is dropped: its output is drained
	quitOne sync.Once
	done    chan struct{} // closed once it exited and was waited for
	cleanup []func()      // its MCP server and config files

	writing sync.Mutex
	open    bool
	once    sync.Once
	idle    *time.Timer
}

// claudeParked are the processes waiting for their chat's next turn.
var claudeParked = struct {
	sync.Mutex
	byChat map[string]*claudeProcess
}{byChat: map[string]*claudeProcess{}}

// startClaudeProcess starts cmd and reads its output.
func startClaudeProcess(cmd *exec.Cmd, key string, binding *todoMCPBinding, cleanup []func()) (*claudeProcess, error) {
	process := &claudeProcess{key: key, cmd: cmd, binding: binding, cleanup: cleanup, stderr: &outputBuffer{},
		lines: make(chan []byte), quit: make(chan struct{}), done: make(chan struct{}), open: true}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	process.stdin = stdin
	cmd.Stderr = process.stderr
	// Kill the tree before exec's fallback kills only its parent on Windows.
	cmd.WaitDelay = 4 * time.Second
	if err := cmd.Start(); err != nil {
		process.release()
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 64<<20)
	read:
		for scanner.Scan() {
			select {
			case process.lines <- append([]byte(nil), scanner.Bytes()...):
			case <-process.quit:
				io.Copy(io.Discard, stdout)
				break read
			}
		}
		cmd.Wait()
		platform.Devf("claude", "process ended: %v", cmd.ProcessState)
		close(process.done)
		close(process.lines)
	}()
	return process, nil
}

func (process *claudeProcess) send(line string) {
	process.writing.Lock()
	defer process.writing.Unlock()
	if process.open {
		io.WriteString(process.stdin, line+"\n")
	}
}

// finish closes its input, after an interrupt if asked: Claude Code exits
// after its last turn.
func (process *claudeProcess) finish(interrupt bool) {
	process.writing.Lock()
	defer process.writing.Unlock()
	if process.open {
		if interrupt {
			io.WriteString(process.stdin, `{"type":"control_request","request_id":"1loop-stop","request":{"subtype":"interrupt","cancel_queued":true}}`+"\n")
		}
		process.open = false
		process.stdin.Close()
	}
}

// alive: not exited, input still open.
func (process *claudeProcess) alive() bool {
	select {
	case <-process.done:
		return false
	default:
	}
	process.writing.Lock()
	defer process.writing.Unlock()
	return process.open
}

// end stops the process: its input closed (interrupted first when asked),
// killed after grace if it is still there, then its MCP server and files
// released. It waits for the exit.
func (process *claudeProcess) end(interrupt bool, grace time.Duration) {
	process.finish(interrupt)
	// Nobody reads its lines any more: the reader drains them, or it would
	// wait to hand one over while this waits for the exit, which it reports
	// (#545: Stop did nothing on a chat's second turn).
	process.stopReading()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-process.done:
	case <-timer.C:
		platform.KillProcessTree(process.cmd)
		<-process.done
	}
	process.release()
}

// kill ends it at once.
func (process *claudeProcess) kill() {
	platform.KillProcessTree(process.cmd)
	process.end(false, 4*time.Second)
}

// stopReading lets the reader drop the rest of the output.
func (process *claudeProcess) stopReading() {
	process.quitOne.Do(func() { close(process.quit) })
}

func (process *claudeProcess) release() {
	process.once.Do(func() {
		process.stopReading()
		for _, clean := range process.cleanup {
			clean()
		}
	})
}

// parkClaude keeps a chat's process for its next turn.
func parkClaude(chat string, process *claudeProcess) {
	claudeParked.Lock()
	defer claudeParked.Unlock()
	if old := claudeParked.byChat[chat]; old != nil && old != process {
		go old.end(false, 3*time.Second)
	}
	claudeParked.byChat[chat] = process
	process.idle = time.AfterFunc(claudeIdleLimit, func() {
		claudeParked.Lock()
		if claudeParked.byChat[chat] == process {
			delete(claudeParked.byChat, chat)
		}
		claudeParked.Unlock()
		platform.Devf("claude", "kept process idle for %v: closing it", claudeIdleLimit)
		process.end(false, 3*time.Second)
	})
}

// takeClaude is the chat's kept process when it fits (same key and session,
// still running); any other kept process of the chat is ended.
func takeClaude(chat, key, session string) *claudeProcess {
	claudeParked.Lock()
	process := claudeParked.byChat[chat]
	delete(claudeParked.byChat, chat)
	claudeParked.Unlock()
	if process == nil {
		return nil
	}
	if process.idle != nil {
		process.idle.Stop()
	}
	if process.key != key || process.session == "" || process.session != session || !process.alive() {
		platform.Devf("claude", "kept process does not fit this turn: starting another")
		go process.end(false, 3*time.Second)
		return nil
	}
	return process
}

// DropClaudeProcess ends a chat's kept process: the chat is closed or
// deleted.
func DropClaudeProcess(chat string) {
	claudeParked.Lock()
	process := claudeParked.byChat[chat]
	delete(claudeParked.byChat, chat)
	claudeParked.Unlock()
	if process != nil {
		if process.idle != nil {
			process.idle.Stop()
		}
		go process.end(false, 3*time.Second)
	}
}

// DropClaudeChat ends the kept processes of a chat, whatever its version.
func DropClaudeChat(id string) {
	claudeParked.Lock()
	var chats []string
	for chat := range claudeParked.byChat {
		if strings.HasPrefix(chat, id) { // id, then its version

			chats = append(chats, chat)
		}
	}
	claudeParked.Unlock()
	for _, chat := range chats {
		DropClaudeProcess(chat)
	}
}

// StopEveryClaude ends every kept process: 1loop quits.
func StopEveryClaude() {
	claudeParked.Lock()
	list := claudeParked.byChat
	claudeParked.byChat = map[string]*claudeProcess{}
	claudeParked.Unlock()
	for _, process := range list {
		if process.idle != nil {
			process.idle.Stop()
		}
		process.kill()
	}
}
