// SPDX-License-Identifier: MPL-2.0

package agent

import (
	"errors"
	"fmt"
	"sync"
)

// ErrPaused ends a run that was asked to pause, between two steps: every
// call has its result, the history is complete (#18).
var ErrPaused = errors.New("The task paused to go on elsewhere")

// PauseSignal asks a run to pause at its next step.
type PauseSignal struct {
	once sync.Once
	ch   chan struct{}
}

func NewPauseSignal() *PauseSignal { return &PauseSignal{ch: make(chan struct{})} }

func (signal *PauseSignal) Ask() {
	if signal != nil {
		signal.once.Do(func() { close(signal.ch) })
	}
}

func (signal *PauseSignal) asked() bool {
	if signal == nil {
		return false
	}
	select {
	case <-signal.ch:
		return true
	default:
		return false
	}
}

// CanPause says a chat's agent can pause between two steps.
func CanPause(provider string) error {
	switch provider {
	case "antigravity", "chatgptweb":
		return fmt.Errorf("%s cannot pause between two steps: wait for its answer, or stop it, then move the chat", ProviderName(provider))
	}
	return nil
}

// MovedMessage starts a moved task again (its first line is the chat's
// note). from is the device it left; note is what went wrong when it goes on
// where it was.
func MovedMessage(from, problem string) Message {
	text := "[Task moved here from " + from + "]\nContinue the task exactly where it stopped, as if nothing had happened: do not start over, do not ask. The project's files, this conversation and the TODO list came along. What did not: commands left running, terminals, browser tabs, granted permissions, environment variables, ignored and generated files (dependencies, build outputs), secrets such as .env. If you need one of them, check and make it again."
	if problem != "" {
		text = "[The task was not moved: " + problem + "]\nIt goes on here, on the same device: continue exactly where it stopped."
	}
	return Message{Role: "user", Internal: true, Content: text}
}
