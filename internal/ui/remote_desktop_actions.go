// SPDX-License-Identifier: MPL-2.0

package ui

// A server window's actions are sent once per click (#591): while a
// control's request is on its way it is disabled, a second click does
// nothing, and the control comes back when the server answers. An error
// shows where the control is, or in a notice, not only in the line under
// the conversation.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/remoteapi"
)

// pendingControl is a control disabled while its request is on its way.
type pendingControl interface {
	Disable()
	Enable()
}

// remoteAction says how act sends a request.
type remoteAction struct {
	key     string         // one request at a time for it; the path and the body when ""
	control pendingControl // disabled until the server answers, nil for none
	latest  bool           // a call meanwhile is kept, and sent once the first is answered (a model chosen, then another)
	reply   any            // where the answer goes, nil to drop it
	wait    time.Duration  // 15 s when 0
	title   string         // of the notice an error shows in, when done is nil
	done    func(error)    // on the window's thread once the server answered; nil shows an error in a notice
}

// remotePending is a request on its way, and the one kept to follow it.
type remotePending struct {
	next *remoteQueued
}

type remoteQueued struct {
	path  string
	value any
	how   remoteAction
}

// act posts value to path unless a request of the same key is on its way:
// it says whether it sent it (or kept it to send next, with latest).
func (remote *remoteDesktop) act(path string, value any, how remoteAction) bool {
	if how.key == "" {
		body, _ := json.Marshal(value)
		how.key = path + " " + string(body)
	}
	if remote.live.pending == nil {
		remote.live.pending = map[string]*remotePending{}
	}
	if pending := remote.live.pending[how.key]; pending != nil {
		if how.latest {
			pending.next = &remoteQueued{path, value, how}
			return true
		}
		return false
	}
	pending := &remotePending{}
	remote.live.pending[how.key] = pending
	if how.control != nil {
		how.control.Disable()
	}
	wait := how.wait
	if wait == 0 {
		wait = 15 * time.Second
	}
	remote.busy(1)
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, wait)
		defer cancel()
		err := remote.client.json(ctx, "POST", path, value, how.reply)
		fyne.Do(func() {
			remote.busy(-1)
			if remote.live.pending[how.key] == pending {
				delete(remote.live.pending, how.key)
			}
			if how.control != nil {
				how.control.Enable()
			}
			if remote.ctx.Err() != nil {
				return
			}
			switch {
			case how.done != nil:
				how.done(err)
			case err != nil:
				remote.failed(how.title, err)
			}
			// What it changed comes in the server's events; an older server
			// that sends fewer is read again.
			parts := wantState
			if strings.HasSuffix(path, "/panel") {
				parts |= wantPanel
			}
			remote.want(parts)
			if next := pending.next; next != nil {
				remote.act(next.path, next.value, next.how)
			}
		})
	}()
	return true
}

// busy counts the requests on their way (remote_activity.go shows them).
func (remote *remoteDesktop) busy(delta int) {
	remote.live.inflight += delta
}

// sending says whether a request of key is on its way.
func (remote *remoteDesktop) sending(key string) bool {
	return remote.live.pending[key] != nil
}

// action posts value to path, once while the same request is on its way;
// an error shows in a notice.
func (remote *remoteDesktop) action(path string, value any) {
	remote.act(path, value, remoteAction{})
}

// failed shows an action's error in a notice, and in the line under the
// conversation.
func (remote *remoteDesktop) failed(title string, err error) {
	if title == "" {
		title = serverName(remote.client.address.URL)
	}
	remote.status.SetText(err.Error())
	remote.connected = "" // the next answer says it is connected again
	remote.parent.notice(title, err.Error())
}

func (remote *remoteDesktop) stop() {
	chat := remote.current
	remote.act("/api/stop", map[string]string{"chat": chat}, remoteAction{key: "stop:" + chat, control: remote.parent.stop, title: "Stop"})
}

// approve answers the server's approval; if that fails, it is asked again.
func (remote *remoteDesktop) approve(id string, allow bool) {
	remote.act("/api/approve", map[string]any{"id": id, "allow": allow}, remoteAction{key: "approve:" + id, done: func(err error) {
		if err == nil {
			return
		}
		remote.failed("Approval", err)
		if remote.approvalID == id && remote.meta.Approval != nil && remote.meta.Approval.ID == id {
			remote.approvalID = "" // shown again by the meta it is still in
			if conv := remote.convs[remote.current]; conv != nil && remote.parent.current == conv {
				remote.showMeta(conv)
			}
		}
	}})
}

// sendKey is the composer's request for a chat ("" for a new one).
func sendKey(chat string) string { return "send:" + chat }

// syncSending disables Send and Steer while the open chat's message is on
// its way.
func (remote *remoteDesktop) syncSending() {
	ui := remote.parent
	if remote.sending(sendKey(remote.current)) {
		ui.send.Disable()
		ui.steer.Disable()
	} else {
		ui.send.Enable()
		ui.steer.Enable()
	}
}

// renameChat gives a server chat a title; the dialog stays until the
// server took it, and says why if it did not.
func (remote *remoteDesktop) renameChat(chat remoteapi.Chat) {
	ui := remote.parent
	entry := widget.NewEntry()
	entry.SetText(chat.Title)
	problem := note("")
	var dialog *sheet
	var rename *tapRow
	apply := func() {
		title := remoteapi.CleanChatTitle(entry.Text)
		if title == "" {
			problem.SetText("A chat needs a title.")
			return
		}
		problem.SetText("")
		remote.act("/api/chats/"+chat.ID+"/rename", map[string]string{"title": title}, remoteAction{key: "rename:" + chat.ID, control: rename, done: func(err error) {
			if err != nil {
				problem.SetText(err.Error())
				return
			}
			dialog.dismiss()
		}})
	}
	entry.OnSubmitted = func(string) { apply() }
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	rename = newRow("Rename", nil, stylePrimary, apply)
	dialog = ui.modal("Rename chat", container.NewVBox(entry, problem), 420, nil, cancel, rename)
	ui.window.Canvas().Focus(entry)
}
