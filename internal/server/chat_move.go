// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/projectsync"
)

// A chat moving to another device, or from one, its agent at work (#18,
// projectsync.Move).

// movable says why a chat cannot move now, or nothing.
func (server *headlessServer) movableLocked(chat *serverChat) error {
	switch {
	case chat == nil:
		return fmt.Errorf("Chat not found")
	case chat.moving:
		return fmt.Errorf("This chat is already moving")
	case chat.Config.MachineID != "":
		return fmt.Errorf("This chat works on an SSH machine: add that machine on the other device instead")
	case chat.Config.Scratch != "":
		return fmt.Errorf("This chat has no project folder: move it to a folder first")
	}
	for _, other := range server.chats {
		if other != chat && other.run != nil && other.Config.Workspace == chat.Config.Workspace {
			return fmt.Errorf("“%s” is also at work in this project: stop it, or wait for it", other.Title)
		}
	}
	if chat.run != nil {
		return agent.CanPause(chat.Config.Provider)
	}
	return nil
}

func (server *headlessServer) pauseChat(ctx context.Context, id string) (projectsync.Paused, error) {
	server.mu.Lock()
	chat := server.chatLocked(id)
	if err := server.movableLocked(chat); err != nil {
		server.mu.Unlock()
		return projectsync.Paused{}, err
	}
	chat.moving = true
	run := chat.run
	server.mu.Unlock()
	if run != nil {
		run.pause.Ask()
		select {
		case <-run.ended:
		case <-ctx.Done():
			server.mu.Lock()
			defer server.mu.Unlock()
			select {
			case <-run.ended: // just now
			default:
				// Asked once, the run pauses at its next step anyway: it is
				// started again then.
				go func() {
					<-run.ended
					server.resumeChat(id, server.name, "the step lasted too long", projectsync.Paused{Working: run.paused})
				}()
				return projectsync.Paused{}, fmt.Errorf("The agent's step is not over: the task stays here")
			}
		}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	paused := projectsync.Paused{Working: run != nil && run.paused, Queue: append(append([]agent.Message{}, chat.steer...), chat.queue...)}
	chat.steer, chat.queue = nil, nil
	paused.Items = len((projectsync.Chat{Config: chat.Config, History: chat.History, Items: chat.Items}).Portable().Items)
	return paused, nil
}

func (server *headlessServer) resumeChat(id, from, problem string, paused projectsync.Paused) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.chatLocked(id)
	if chat == nil {
		return fmt.Errorf("The conversation did not arrive")
	}
	if chat.run != nil {
		return fmt.Errorf("This conversation is already at work here")
	}
	if problem == "" {
		if here := len((projectsync.Chat{Config: chat.Config, History: chat.History, Items: chat.Items}).Portable().Items); here != paused.Items {
			return fmt.Errorf("The conversation here is not the one that left")
		}
		if chat.Config.Model == "" {
			return fmt.Errorf("Configure a provider and model on the server first")
		}
	}
	chat.moving = false
	chat.queue = append(append([]agent.Message{}, paused.Queue...), chat.queue...)
	switch {
	case paused.Working:
		server.runLocked(chat, agent.MovedMessage(from, problem))
	case len(chat.queue) > 0:
		next := chat.queue[0]
		chat.queue = chat.queue[1:]
		server.runLocked(chat, next)
	}
	server.scheduleSaveLocked()
	return nil
}

func (server *headlessServer) movedChat(id, to string) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	chat := server.chatLocked(id)
	if chat == nil {
		return nil
	}
	chat.moving = false
	index := server.addLocked(chat, agent.ChatItem{Kind: "note", Text: projectsync.MovedNote(to)})
	server.changedLocked(chat, index)
	server.broadcastLocked("chats", server.chatsEventLocked())
	server.scheduleSaveLocked()
	return nil
}

// localMove is POST /api/local/move, the phone app's: a chat with its agent,
// between this phone and a paired desktop or server.
func (server *headlessServer) localMove(w http.ResponseWriter, r *http.Request) {
	var args struct {
		Peer      projectsync.Peer
		PeerName  string
		Direction string // "pull" (to this phone) or "push"
		Local     string
		Remote    string
		Chat      string
	}
	if core.ReadJSON(r, &args) != nil || args.Peer.URL == "" || args.Peer.Token == "" || args.Chat == "" || args.Direction != "push" && args.Direction != "pull" {
		core.Fail(w, 400, "Invalid move request")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Hour)
	defer cancel()
	here := server.syncEngine()
	bases := &projectsync.BaseStore{Path: filepath.Join(server.dir, "sync-bases.json")}
	quiet := func(string) {}
	var report projectsync.Report
	var err error
	if args.Direction == "push" {
		report, err = projectsync.Move(ctx, here, "this phone", args.Local, &args.Peer, args.PeerName, args.Remote, args.Chat, bases, quiet)
	} else {
		report, err = projectsync.Move(ctx, &args.Peer, args.PeerName, args.Remote, here, "this phone", args.Local, args.Chat, bases, quiet)
	}
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]any{"report": report, "summary": report.Summary()})
}
