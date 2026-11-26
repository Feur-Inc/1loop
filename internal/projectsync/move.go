// SPDX-License-Identifier: MPL-2.0

package projectsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"1loop/internal/agent"
)

// Moving a chat, its agent at work, to another engine (#18): the agent
// pauses between two steps, the project is copied, the agent goes on there.
// The task is never at work on both sides: it only goes on here when the
// other side surely did not start it.

// Paused is a chat ready to move.
type Paused struct {
	Working bool            `json:"working"`         // it was at work, and goes on there
	Items   int             `json:"items"`           // of its conversation, to recognize it there
	Queue   []agent.Message `json:"queue,omitempty"` // messages waiting for their turn
}

// Mover is an engine whose chats can move.
type Mover interface {
	Engine
	// Pause waits for the chat's agent to pause. Until Resume or Moved, the
	// chat starts nothing.
	Pause(ctx context.Context, chat string) (Paused, error)
	// Resume goes on with a chat that came from another engine (from), or
	// that could not leave (problem).
	Resume(ctx context.Context, chat, from, problem string, paused Paused) error
	// Moved notes where a chat went.
	Moved(ctx context.Context, chat, to string) error
}

// pauseLimit is how long a step may last before the move is given up: the
// task then goes on where it is.
const pauseLimit = 10 * time.Minute

// Move moves chat with its project. status says what is going on.
func Move(ctx context.Context, from Mover, fromName, fromProject string, to Mover, toName, toProject, chat string, bases *BaseStore, status func(string)) (Report, error) {
	status("Checking " + toName + "…")
	if _, err := to.Manifest(ctx, toProject); err != nil {
		return Report{}, fmt.Errorf("%s: %w", toName, err)
	}
	status("Waiting for the agent to finish its step…")
	waiting, cancel := context.WithTimeout(ctx, pauseLimit)
	paused, err := from.Pause(waiting, chat)
	cancel()
	if err != nil {
		return Report{}, err
	}
	// Whatever happens to the caller, the paused task is taken up again.
	back := func(problem error) error {
		later, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := from.Resume(later, chat, fromName, problem.Error(), paused); err != nil {
			return fmt.Errorf("%w. The task is paused on %s and could not go on (%v): send it a message", problem, fromName, err)
		}
		return problem
	}
	status("Comparing both sides…")
	report, err := Copy(ctx, from, fromProject, to, toProject, bases, true)
	if err != nil {
		return report, back(err)
	}
	if slices.Contains(report.Plan.ChatConflicts, chat) {
		return report, back(fmt.Errorf("This conversation has also changed on %s: go on with it there, or copy the project to keep both", toName))
	}
	status("Copying to " + toName + ": " + report.Summary())
	if report, err = Copy(ctx, from, fromProject, to, toProject, bases, false); err != nil {
		return report, back(err)
	}
	if paused.Working || len(paused.Queue) > 0 {
		status("Starting again on " + toName + "…")
		if err := to.Resume(ctx, chat, fromName, "", paused); err != nil {
			var refused *peerError
			_, local := to.(*Local)
			if local || errors.As(err, &refused) { // it said no: it is not running there
				return report, back(fmt.Errorf("%s: %w", toName, err))
			}
			// No answer: it may have started. Two agents on the same task
			// would be worse than one paused.
			from.Moved(context.WithoutCancel(ctx), chat, toName)
			return report, fmt.Errorf("%s did not answer after receiving the task (%v): look there whether it goes on. It is paused on %s", toName, err, fromName)
		}
	}
	later, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := from.Moved(later, chat, toName); err != nil {
		return report, fmt.Errorf("The task went on to %s, but %s could not note it: %w", toName, fromName, err)
	}
	return report, nil
}

func (local *Local) Pause(ctx context.Context, chat string) (Paused, error) {
	return local.PauseChat(ctx, chat)
}

func (local *Local) Resume(_ context.Context, chat, from, problem string, paused Paused) error {
	return local.ResumeChat(chat, from, problem, paused)
}

func (local *Local) Moved(_ context.Context, chat, to string) error {
	return local.MovedChat(chat, to)
}

func (peer *Peer) post(ctx context.Context, chat, action string, body, reply any) error {
	data, _ := json.Marshal(body)
	response, err := peer.request(ctx, "POST", "/api/chats/"+url.PathEscape(chat)+"/"+action, bytes.NewReader(data), "application/json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if reply == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(reply)
}

func (peer *Peer) Pause(ctx context.Context, chat string) (paused Paused, err error) {
	err = peer.post(ctx, chat, "pause", struct{}{}, &paused)
	return paused, err
}

// ResumeRequest is the body of POST /api/chats/{id}/resume.
type ResumeRequest struct {
	From    string `json:"from"`
	Problem string `json:"problem,omitempty"`
	Paused  Paused `json:"paused"`
}

func (peer *Peer) Resume(ctx context.Context, chat, from, problem string, paused Paused) error {
	return peer.post(ctx, chat, "resume", ResumeRequest{from, problem, paused}, nil)
}

func (peer *Peer) Moved(ctx context.Context, chat, to string) error {
	return peer.post(ctx, chat, "moved", map[string]string{"to": to}, nil)
}

// MovedNote is the note a chat that left keeps.
func MovedNote(to string) string {
	return movedPrefix + to + ". The task goes on there; a message here continues it on this device instead."
}

const movedPrefix = "Moved to "
