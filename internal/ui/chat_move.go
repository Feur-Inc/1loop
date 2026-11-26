// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"

	"1loop/internal/agent"
	"1loop/internal/projectsync"
)

// A chat moving to another device, or from one, its agent at work (#18,
// projectsync.Move).

func (ui *desktopUI) movable(chat *conversation) error {
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
	for _, other := range ui.chats {
		if other != chat && other.run != nil && other.Config.Workspace == chat.Config.Workspace {
			return fmt.Errorf("“%s” is also at work in this project: stop it, or wait for it", other.Title)
		}
	}
	if chat.run != nil {
		if chat.run.pause == nil {
			return fmt.Errorf("This chat is compacting its context: wait for it")
		}
		return agent.CanPause(chat.Config.Provider)
	}
	return nil
}

func (ui *desktopUI) uiDo(f func()) {
	if ui.ctx.Err() == nil {
		fyne.DoAndWait(f)
	}
}

func (ui *desktopUI) pauseChat(ctx context.Context, id string) (projectsync.Paused, error) {
	var run *chatRun
	var chat *conversation
	err := fmt.Errorf("1loop is closing")
	ui.uiDo(func() {
		chat = ui.chatByID(id)
		if err = ui.movable(chat); err == nil {
			chat.moving, run = true, chat.run
		}
	})
	if err != nil {
		return projectsync.Paused{}, err
	}
	if run != nil {
		run.pause.Ask()
		select {
		case <-run.ended:
		case <-ctx.Done():
			select {
			case <-run.ended: // just now
			default:
				// Asked once, the run pauses at its next step anyway: it is
				// started again then.
				go func() {
					<-run.ended
					ui.resumeMoved(id, "this computer", "the step lasted too long", projectsync.Paused{Working: run.paused})
				}()
				return projectsync.Paused{}, fmt.Errorf("The agent's step is not over: the task stays here")
			}
		}
	}
	var paused projectsync.Paused
	ui.uiDo(func() {
		paused = projectsync.Paused{Working: run != nil && run.paused, Queue: append(append([]agent.Message{}, chat.steer...), chat.queue...)}
		chat.steer, chat.queue = nil, nil
		paused.Items = len((projectsync.Chat{Config: chat.Config, History: chat.History, Items: chat.Items}).Portable().Items)
		if chat == ui.viewOf(chat).current {
			ui.viewOf(chat).renderQueue()
		}
	})
	return paused, nil
}

func (ui *desktopUI) resumeMoved(id, from, problem string, paused projectsync.Paused) error {
	err := fmt.Errorf("1loop is closing")
	ui.uiDo(func() {
		chat := ui.chatByID(id)
		switch {
		case chat == nil:
			err = fmt.Errorf("The conversation did not arrive")
			return
		case chat.run != nil:
			err = fmt.Errorf("This conversation is already at work here")
			return
		}
		if problem == "" {
			if here := len((projectsync.Chat{Config: chat.Config, History: chat.History, Items: chat.Items}).Portable().Items); here != paused.Items {
				err = fmt.Errorf("The conversation here is not the one that left")
				return
			}
			if chat.Config.Model == "" {
				err = fmt.Errorf("Choose a model on this computer first")
				return
			}
		}
		err = nil
		chat.moving = false
		chat.queue = append(append([]agent.Message{}, paused.Queue...), chat.queue...)
		if paused.Working {
			ui.startIn(chat, agent.MovedMessage(from, problem), "")
		} else if next, ok := ui.takeNext(chat); ok {
			ui.startIn(chat, next, "")
		}
		ui.save()
		ui.renderChats()
	})
	return err
}

func (ui *desktopUI) movedChat(id, to string) error {
	ui.uiDo(func() {
		if chat := ui.chatByID(id); chat != nil {
			chat.moving = false
			ui.addItem(chat, agent.ChatItem{Kind: "note", Text: projectsync.MovedNote(to)})
			ui.save()
			ui.renderChats()
		}
	})
	return nil
}

// moveActions are the chat menu's "Move to …", one per paired server.
func (ui *desktopUI) moveActions(chat *conversation) []menuAction {
	var actions []menuAction
	if ui.home != "" || chat.Config.Workspace == "" {
		return nil
	}
	for _, address := range loadServerAddresses() {
		if address.Token == "" {
			continue
		}
		actions = append(actions, menuAction{label: "Move to " + serverName(address.URL) + "…", icon: theme.ComputerIcon(), run: func() {
			peer := &projectsync.Peer{URL: address.URL, Token: address.Token, Fingerprint: address.Fingerprint}
			ui.moveDialog(chat.Title, chat.run != nil, serverName(address.URL), func(ctx context.Context, status func(string)) (projectsync.Report, error) {
				here := ui.syncEngine()
				return projectsync.Move(ctx, here, here.Device, chat.Config.Workspace, peer, serverName(address.URL), filepath.Base(chat.Config.Workspace), chat.ID, here.Bases, status)
			}, func() { ui.openServerChat(address, chat.ID) })
		}})
	}
	return actions
}

// moveHere brings a server's chat to this computer.
func (remote *remoteDesktop) moveHere(id, title, workspace string, working bool) {
	ui := remote.parent
	address := remote.client.address
	peer := &projectsync.Peer{URL: address.URL, Token: address.Token, Fingerprint: address.Fingerprint}
	device, _ := os.Hostname()
	ui.moveDialog(title, working, "this computer", func(ctx context.Context, status func(string)) (projectsync.Report, error) {
		here := ui.syncEngine()
		return projectsync.Move(ctx, peer, serverName(address.URL), workspace, here, device, path.Base(strings.ReplaceAll(workspace, "\\", "/")), id, here.Bases, status)
	}, func() {
		ui.useComputer()
		if chat := ui.chatByID(id); chat != nil {
			ui.openChat(chat)
		}
	})
}

// openServerChat shows a chat of a paired server.
func (ui *desktopUI) openServerChat(address serverAddress, id string) {
	if ui.server == nil || ui.server.client.address.URL != address.URL {
		client, err := newRemoteClient(address)
		if err != nil {
			ui.notice("Move", err.Error())
			return
		}
		ui.useServer(client)
	}
	remote := ui.server
	go func() {
		for range 40 { // once the server's chats are read
			done := false
			fyne.DoAndWait(func() {
				if ui.server != remote {
					done = true
					return
				}
				for _, chat := range remote.chats {
					if chat.ID == id {
						remote.openChat(id)
						done = true
					}
				}
			})
			if done {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()
}

// moveDialog says what a move does, then how it goes. It goes on when the
// dialog is closed.
func (ui *desktopUI) moveDialog(title string, working bool, to string, move func(context.Context, func(string)) (projectsync.Report, error), open func()) {
	about := "“" + title + "” moves to " + to + " with its project: files that changed, this conversation and its tasks."
	if working {
		about += " The agent finishes its current step, pauses, and goes on there with that device's model and approval mode."
	}
	about += " Commands left running, terminals, browser tabs, ignored files and secrets (.env, keys) stay here."
	status := note("")
	status.Hide()
	var dialog *sheet
	var start, cancel *tapRow
	start = newRow("Move", nil, stylePrimary, nil)
	start.onTap = func() {
		start.Disable()
		cancel.SetText("Close")
		status.SetText("Starting…")
		status.Show()
		go func() {
			ctx, stop := context.WithTimeout(ui.ctx, time.Hour)
			defer stop()
			_, err := move(ctx, func(text string) { fyne.Do(func() { status.SetText(text) }) })
			fyne.Do(func() {
				if ui.ctx.Err() != nil {
					return
				}
				if err != nil {
					status.SetText(err.Error())
					if !dialog.shown() {
						ui.notice("Move", err.Error())
					}
					return
				}
				shown := dialog.shown()
				dialog.dismiss()
				if shown {
					open()
				}
			})
		}()
	}
	cancel = newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal("Move to "+to, container.NewVBox(note(about), status), 560, nil, cancel, start)
}
