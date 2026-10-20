package ui

// The desktop as a client of a headless server (docs/SERVER.md): connecting and
// pairing, then the server's chats in a window of their own.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

func remoteAddressPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "1loop", "remote-server.json")
}

func (ui *desktopUI) connectServer() {
	var saved serverAddress
	if data, err := os.ReadFile(remoteAddressPath()); err == nil {
		json.Unmarshal(data, &saved)
	}
	address, pin := widget.NewEntry(), widget.NewEntry()
	address.SetText(saved.URL)
	address.SetPlaceHolder("https://192.168.1.20:47101")
	pin.SetText(saved.Fingerprint)
	pin.SetPlaceHolder("SHA-256 fingerprint from the server console")
	var sheet *sheet
	connect := newRow("Connect", nil, stylePrimary, nil)
	connect.onTap = func() {
		target := serverAddress{URL: address.Text, Fingerprint: pin.Text}
		if target.URL == saved.URL && target.Fingerprint == saved.Fingerprint {
			target.Token = saved.Token
		}
		client, err := newRemoteClient(target)
		if err != nil {
			ui.notice("Remote server", err.Error())
			return
		}
		connect.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 10*time.Second)
			defer cancel()
			if target.Token != "" {
				var state map[string]any
				err = client.json(ctx, "GET", "/api/state", nil, &state)
				if err == nil {
					fyne.Do(func() { sheet.dismiss(); openRemoteDesktop(ui, client) })
					return
				}
			}
			var reply struct {
				Pairing, Code string
				Console       bool
			}
			err = client.json(ctx, "POST", "/api/pair", map[string]string{"device": "1loop desktop"}, &reply)
			fyne.Do(func() {
				connect.Enable()
				if err != nil {
					ui.notice("Remote server", err.Error())
					return
				}
				if !reply.Console {
					ui.notice("Remote server", "This address is a desktop's phone remote. Connect to a headless HTTPS server.")
					return
				}
				sheet.dismiss()
				ui.confirmServerPair(client, reply.Pairing, reply.Code)
			})
		}()
	}
	sheet = ui.modal("Connect to a remote server", container.NewVBox(note("The server runs your model, browser and terminal on its machine. For a private certificate, copy its SHA-256 fingerprint from the VM console before connecting."), widget.NewLabel("HTTPS address"), address, widget.NewLabel("Certificate fingerprint"), pin), 620, nil, connect)
}

func (ui *desktopUI) confirmServerPair(client *remoteClient, pairingID, code string) {
	var sheet *sheet
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	command := "pair " + pairingID + " " + code
	confirm := newRow("Connect after approval", nil, stylePrimary, nil)
	confirm.onTap = func() {
		confirm.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 10*time.Second)
			defer cancel()
			var reply struct{ Token string }
			err := client.json(ctx, "POST", "/api/pair/confirm", map[string]string{"pairing": pairingID, "code": code}, &reply)
			fyne.Do(func() {
				confirm.Enable()
				if err != nil {
					status.SetText(err.Error())
					return
				}
				client.address.Token = reply.Token
				data, _ := json.Marshal(client.address)
				path := remoteAddressPath()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err == nil {
					err = os.WriteFile(path, data, 0600)
					if err != nil {
						status.SetText(err.Error())
						return
					}
				} else {
					status.SetText(err.Error())
					return
				}
				sheet.dismiss()
				openRemoteDesktop(ui, client)
			})
		}()
	}
	copyCommand := newRow("Copy command", nil, styleChip, func() { fyne.CurrentApp().Clipboard().SetContent(command) })
	body := container.NewVBox(
		note("Type this command in the VM's server console. The pairing expires after two minutes."),
		codeBlock(command, 80),
		copyCommand,
		status,
	)
	sheet = ui.modal("Approve this desktop on the server", body, 620, nil, confirm)
}

type remoteDesktop struct {
	renderedApproval        string
	client                  *remoteClient
	ctx                     context.Context
	cancel                  context.CancelFunc
	window                  fyne.Window
	current                 string
	chats                   []remoteapi.Chat
	meta                    remoteapi.Meta
	models                  []remoteapi.Model
	chatList, transcript    *fyne.Container
	scroll                  *container.Scroll
	prompt                  *chatEntry
	status                  *widget.Label
	model, effort           *widget.Select
	approval                *widget.Select
	tabs                    *container.DocTabs
	tabViews                map[string]fyne.CanvasObject
	tabCancel               map[string]context.CancelFunc
	wake                    chan struct{}
	rebuilding              bool
	renderedItems           []remoteapi.Item
	messageBox, approvalBox *fyne.Container
	tabActivate             map[string]func(bool)
	hiddenTabs              map[string]bool
	todoTasks               *fyne.Container
	todoFooter              *widget.Label
	todoRows                map[string]*widget.Label
	todoSignature           string
}

func openRemoteDesktop(parent *desktopUI, client *remoteClient) {
	ctx, cancel := context.WithCancel(parent.ctx)
	remote := &remoteDesktop{
		client:      client,
		ctx:         ctx,
		cancel:      cancel,
		wake:        make(chan struct{}, 1),
		tabViews:    map[string]fyne.CanvasObject{},
		tabCancel:   map[string]context.CancelFunc{},
		tabActivate: map[string]func(bool){},
		hiddenTabs:  map[string]bool{},
	}
	remote.window = fyne.CurrentApp().NewWindow("1loop · " + client.address.URL)
	remote.window.Resize(fyne.NewSize(1500, 950))
	remote.chatList = container.NewVBox()
	remote.messageBox, remote.approvalBox = container.NewVBox(), container.NewVBox()
	remote.transcript = container.NewVBox(remote.messageBox, remote.approvalBox)
	remote.scroll = container.NewVScroll(remote.transcript)
	remote.status = widget.NewLabel("Connecting…")
	remote.status.Wrapping = fyne.TextWrapWord
	remote.prompt = newChatEntry()
	remote.prompt.SetPlaceHolder("Describe what you want to build on the server…")
	remote.prompt.send = func() { remote.send("send") }
	remote.prompt.steer = func() { remote.send("steer") }
	remote.model = widget.NewSelect(nil, func(label string) {
		if !remote.rebuilding {
			remote.action("/api/model", map[string]string{"chat": remote.current, "label": label})
		}
	})
	remote.effort = widget.NewSelect(nil, func(value string) {
		if !remote.rebuilding {
			remote.action("/api/model", map[string]string{"chat": remote.current, "effort": value})
		}
	})
	remote.approval = widget.NewSelect([]string{"approval", "yolo", "auto"}, func(value string) {
		if !remote.rebuilding {
			remote.action("/api/approval-mode", map[string]string{"mode": value})
		}
	})
	send := newRow("Send", nil, stylePrimary, func() { remote.send("send") })
	stop := newRow("Stop", nil, styleOutline, func() { remote.action("/api/stop", map[string]string{"chat": remote.current}) })
	controls := container.NewHBox(remote.approval, remote.model, remote.effort, stop, send)
	composer := surface(container.NewVBox(remote.prompt, controls), colSurface, colBorderHot, 14, 12)
	newChat := newRow("New chat", iconPlus, styleChip, func() { remote.selectChat("") })
	refresh := newRow("Refresh", nil, styleChip, remote.notify)
	left := container.NewBorder(container.NewVBox(newChat, refresh), nil, nil, nil, container.NewVScroll(remote.chatList))
	chat := container.NewBorder(nil, container.NewVBox(remote.status, composer), nil, nil, remote.scroll)
	remote.tabs = container.NewDocTabs()
	remote.tabs.OnSelected = func(selected *container.TabItem) {
		for key, activate := range remote.tabActivate {
			activate(remote.tabViews[key] == selected.Content)
		}
	}
	remote.tabs.OnClosed = func(closed *container.TabItem) {
		for key, view := range remote.tabViews {
			if view == closed.Content {
				remote.tabCancel[key]()
				delete(remote.tabCancel, key)
				delete(remote.tabActivate, key)
				delete(remote.tabViews, key)
				remote.hiddenTabs[key] = true
				if strings.HasPrefix(key, "browser-") || strings.HasPrefix(key, "terminal-") {
					remote.action("/api/chats/"+remote.current+"/panel", map[string]string{"action": "close", "key": key})
				}
				break
			}
		}
	}
	add := newRow("+", nil, styleChip, nil)
	add.onTap = func() {
		showMenu(remote.window, add, []menuAction{
			{label: "Tasks", icon: iconCheck, run: func() { delete(remote.hiddenTabs, "todo"); remote.notify() }},
			{label: "Git changes", icon: toolGlyph("edit_file"), run: func() { delete(remote.hiddenTabs, "changes"); remote.notify() }},
			{label: "Browser", icon: iconBrowser, run: func() { remote.addTab("browser") }},
			{label: "Terminal", icon: toolGlyph("shell"), run: func() { remote.addTab("terminal") }},
		})
	}
	panel := container.NewBorder(container.NewHBox(textLine("Server tools", 14, colMuted), layout.NewSpacer(), add), nil, nil, nil, remote.tabs)
	main := container.NewHSplit(chat, panel)
	main.Offset = .5
	split := container.NewHSplit(left, main)
	split.Offset = .17
	remote.window.SetContent(padded(12, 12, 12, 12, split))
	remote.window.SetOnClosed(func() {
		remote.cancel()
		for _, stop := range remote.tabCancel {
			stop()
		}
	})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fyne.Do(func() {
					if ctx.Err() == nil {
						remote.renderTasks()
					}
				})
			}
		}
	}()
	go remote.refreshLoop()
	go remote.events()
	remote.notify()
	remote.window.Show()
}

func (remote *remoteDesktop) notify() {
	select {
	case remote.wake <- struct{}{}:
	default:
	}
}

func (remote *remoteDesktop) selectChat(id string) {
	remote.current = id
	for _, cancel := range remote.tabCancel {
		cancel()
	}
	remote.tabCancel = map[string]context.CancelFunc{}
	remote.tabViews = map[string]fyne.CanvasObject{}
	remote.tabActivate = map[string]func(bool){}
	remote.hiddenTabs = map[string]bool{}
	remote.tabs.SetItems(nil)
	remote.messageBox.RemoveAll()
	remote.approvalBox.RemoveAll()
	remote.renderedItems = nil
	remote.renderedApproval = ""
	remote.notify()
}

func (remote *remoteDesktop) action(path string, value any) {
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
		defer cancel()
		err := remote.client.json(ctx, "POST", path, value, nil)
		if err != nil {
			fyne.Do(func() { remote.status.SetText(err.Error()) })
		}
		remote.notify()
	}()
}

func (remote *remoteDesktop) send(mode string) {
	text := strings.TrimSpace(remote.prompt.Text)
	if text == "" {
		return
	}
	remote.prompt.SetText("")
	chat := remote.current
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
		defer cancel()
		var reply struct{ Chat string }
		err := remote.client.json(ctx, "POST", "/api/send", map[string]string{"chat": chat, "text": text, "mode": mode}, &reply)
		fyne.Do(func() {
			if err != nil {
				remote.status.SetText(err.Error())
				remote.prompt.SetText(text)
				return
			}
			if remote.current == chat && reply.Chat != "" && chat == "" {
				remote.selectChat(reply.Chat)
			}
			remote.notify()
		})
	}()
}

func (remote *remoteDesktop) addTab(kind string) {
	if remote.current == "" {
		remote.status.SetText("Start a conversation before opening server tools.")
		return
	}
	remote.action("/api/chats/"+remote.current+"/panel", map[string]string{"action": "open", "kind": kind})
}

func (remote *remoteDesktop) events() {
	for remote.ctx.Err() == nil {
		response, err := remote.client.request(remote.ctx, "GET", "/api/events", nil)
		if err == nil {
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(make([]byte, 8192), 32<<20)
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), "data:") {
					remote.notify()
				}
			}
			response.Body.Close()
		}
		remote.notify()
		select {
		case <-remote.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (remote *remoteDesktop) refreshLoop() {
	for {
		select {
		case <-remote.ctx.Done():
			return
		case <-remote.wake:
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-remote.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		var id string
		fyne.DoAndWait(func() { id = remote.current })
		ctx, cancel := context.WithTimeout(remote.ctx, 10*time.Second)
		var state struct {
			Name, Workspace string
			Chats           []remoteapi.Chat
			Settings        struct {
				Models                      []remoteapi.Model
				Model, Effort, ApprovalMode string
				AutoMode                    bool // the server's model has an Auto mode
			}
		}
		err := remote.client.json(ctx, "GET", "/api/state", nil, &state)
		var chat struct {
			Items []remoteapi.Item
			Meta  remoteapi.Meta
		}
		var tabs struct{ Tabs []remoteapi.Tab }
		if err == nil && id != "" {
			err = remote.client.json(ctx, "GET", "/api/chats/"+id, nil, &chat)
			if err == nil {
				err = remote.client.json(ctx, "GET", "/api/chats/"+id+"/panel", nil, &tabs)
			}
		}
		cancel()
		if remote.ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if remote.current != id || remote.ctx.Err() != nil {
				return
			}
			if err != nil {
				remote.status.SetText(err.Error())
				return
			}
			remote.rebuilding = true
			defer func() { remote.rebuilding = false }()
			remote.models = state.Settings.Models

			remote.meta = chat.Meta
			if !slices.Equal(remote.chats, state.Chats) {
				remote.chats = state.Chats
				remote.chatList.RemoveAll()
				for _, summary := range state.Chats {
					summary := summary
					title := summary.Title
					if summary.Running {
						title += " · working"
					}
					remote.chatList.Add(newRow(title, nil, styleChip, func() { remote.selectChat(summary.ID) }))
				}
			}
			labels := []string{}
			for _, model := range remote.models {
				labels = append(labels, model.Label)
			}
			sort.Strings(labels)
			remote.model.Options = labels
			modelLabel, effort := state.Settings.Model, state.Settings.Effort
			if id != "" {
				modelLabel, effort = chat.Meta.Model, chat.Meta.Effort
			}
			remote.model.SetSelected(modelLabel)
			efforts := []string{}
			for _, model := range remote.models {
				if model.Label == modelLabel {
					for _, entry := range model.Efforts {
						efforts = append(efforts, entry.Value)
					}
				}
			}
			remote.effort.Options = efforts
			remote.effort.PlaceHolder = "Default"
			if len(efforts) == 0 {
				remote.effort.Disable()
			} else {
				remote.effort.Enable()
			}
			remote.effort.SetSelected(effort)
			remote.approval.Options = []string{"approval", "yolo"}
			if state.Settings.AutoMode {
				remote.approval.Options = append(remote.approval.Options, "auto")
			}
			remote.approval.SetSelected(state.Settings.ApprovalMode)
			remote.renderTranscript(chat.Items)
			status := "Connected to " + state.Name + " · " + state.Workspace
			if chat.Meta.Running {
				status += " · " + chat.Meta.Label
			}
			if chat.Meta.Window > 0 {
				status += fmt.Sprintf(" · context %d%%", chat.Meta.Used*100/chat.Meta.Window)
			}
			remote.status.SetText(status)
			remote.syncTabs(tabs.Tabs)
		})
	}
}

func (remote *remoteDesktop) renderTranscript(items []remoteapi.Item) {
	following := remote.scroll.Offset.Y+remote.scroll.Size().Height >= remote.transcript.MinSize().Height-40
	changed := false
	if len(items) < len(remote.renderedItems) {
		remote.messageBox.Objects = remote.messageBox.Objects[:len(items)]
		remote.renderedItems = remote.renderedItems[:len(items)]
		changed = true
	}
	for index, item := range items {
		if index < len(remote.renderedItems) && remote.renderedItems[index] == item {
			continue
		}
		var view fyne.CanvasObject
		switch item.Kind {
		case "tool":
			item := item
			view = newRow(item.Verb+" "+item.Target, toolGlyph(item.Tool), styleChip, func() { remote.showTool(item) })
		case "user":
			view = surface(newMarkdownView(item.Text).box, colRaised, colBorderSoft, 12, 12)
		default:
			view = newMarkdownView(item.Text).box
		}
		if item.Images > 0 { // what the model saw, or what the user attached
			view = container.NewVBox(view, remote.itemImages(index, item.Images))
		}
		if index < len(remote.messageBox.Objects) {
			remote.messageBox.Objects[index] = view
			remote.renderedItems[index] = item
		} else {
			remote.messageBox.Objects = append(remote.messageBox.Objects, view)
			remote.renderedItems = append(remote.renderedItems, item)
		}
		changed = true
	}
	if changed {
		remote.messageBox.Refresh()
	}
	approvalID := ""
	if remote.meta.Approval != nil {
		approvalID = remote.meta.Approval.ID
	}
	if remote.meta.Question != nil {
		approvalID += "?" + remote.meta.Question.ID
	}
	if remote.renderedApproval != approvalID {
		remote.renderedApproval = approvalID
		remote.approvalBox.RemoveAll()
		if approval := remote.meta.Approval; approval != nil {
			answer := func(allow bool) func() {
				return func() { remote.action("/api/approve", map[string]any{"id": approval.ID, "allow": allow}) }
			}
			buttons := container.NewHBox(newRow("Deny", nil, styleOutline, answer(false)), newRow("Allow", nil, stylePrimary, answer(true)))
			body := container.NewVBox(note(approval.Title), codeBlock(approval.Preview, 200), buttons)
			remote.approvalBox.Add(surface(body, colSurface, colBorderHot, 12, 12))
		}
		if question := remote.meta.Question; question != nil {
			remote.approvalBox.Add(surface(remote.questionForm(question), colSurface, colBorderHot, 12, 12))
		}
	}
	if changed && following {
		remote.scroll.ScrollToBottom()
	}
}

// questionForm answers the model's questions on the server: an option per
// question, or the user's own words.
func (remote *remoteDesktop) questionForm(question *remoteapi.Question) fyne.CanvasObject {
	answers := map[string]string{}
	form := container.NewVBox()
	for _, q := range question.Questions {
		q := q
		var options []string
		for _, option := range q.Options {
			options = append(options, option.Label)
		}
		other := widget.NewEntry()
		other.SetPlaceHolder("Other: your own answer")
		choice := widget.NewRadioGroup(options, func(label string) {
			if label != "" {
				answers[q.ID] = label
				other.SetText("")
			}
		})
		other.OnChanged = func(text string) {
			if strings.TrimSpace(text) != "" {
				answers[q.ID] = agent.OtherAnswer + strings.TrimSpace(text)
				choice.SetSelected("")
			}
		}
		form.Add(note(q.Question))
		form.Add(choice)
		form.Add(other)
	}
	form.Add(newRow("Send answers", nil, stylePrimary, func() {
		remote.action("/api/answer", map[string]any{"id": question.ID, "answers": answers})
	}))
	return form
}

func (remote *remoteDesktop) showTool(item remoteapi.Item) {
	content := container.NewVScroll(container.NewVBox(note(item.Args), codeBlock(item.Text, 600)))
	if item.Diff != "" {
		content = container.NewVScroll(newDiffView(item.Diff))
	}
	tab := container.NewTabItem(item.Tool, content)
	remote.tabs.Append(tab)
	remote.tabs.Select(tab)
}

func (remote *remoteDesktop) syncTabs(tabs []remoteapi.Tab) {
	present := map[string]bool{}
	for _, tab := range tabs {
		present[tab.Key] = true
	}
	for key, view := range remote.tabViews {
		if !present[key] {
			remote.tabCancel[key]()
			delete(remote.tabCancel, key)
			delete(remote.tabActivate, key)
			delete(remote.tabViews, key)
			for _, item := range remote.tabs.Items {
				if item.Content == view {
					remote.tabs.Remove(item)
					break
				}
			}
		}
	}
	for _, tab := range tabs {
		if strings.HasPrefix(tab.Key, "tool-") || remote.hiddenTabs[tab.Key] {
			continue
		}
		if _, exists := remote.tabViews[tab.Key]; exists {
			continue
		}
		content, cancel, activate := remote.panelContent(tab)
		remote.tabViews[tab.Key] = content
		remote.tabCancel[tab.Key] = cancel
		remote.tabActivate[tab.Key] = activate
		remote.tabs.Append(container.NewTabItem(tab.Title, content))
	}
	remote.renderTasks()
}

// itemImages are an item's pictures as the server serves them; a click
// shows one large.
func (remote *remoteDesktop) itemImages(index, count int) fyne.CanvasObject {
	row := container.NewHBox()
	for n := 0; n < count; n++ {
		view := canvas.NewImageFromImage(nil)
		view.FillMode = canvas.ImageFillContain
		view.SetMinSize(fyne.NewSize(200, 130))
		var picture image.Image
		row.Add(surface(container.NewStack(view, newTapArea(func() {
			if picture != nil {
				remote.showImage(picture)
			}
		})), colCode, colBorderSoft, 8, 0))
		path := fmt.Sprintf("/api/chats/%s/items/%d/images/%d", remote.current, index, n)
		go func() {
			ctx, cancel := context.WithTimeout(remote.ctx, 30*time.Second)
			defer cancel()
			response, err := remote.client.request(ctx, "GET", path, nil)
			if err != nil {
				return
			}
			defer response.Body.Close()
			decoded, _, err := image.Decode(io.LimitReader(response.Body, 32<<20))
			if err != nil {
				return
			}
			fyne.Do(func() {
				picture = decoded
				view.Image = decoded
				view.Refresh()
			})
		}()
	}
	return row
}

func (remote *remoteDesktop) showImage(picture image.Image) {
	view := canvas.NewImageFromImage(picture)
	view.FillMode = canvas.ImageFillContain
	size := remote.window.Canvas().Size()
	// The sheet adds its own padding around the picture and its button.
	view.SetMinSize(fyne.NewSize(size.Width*0.66, size.Height*0.62))
	var dialog *sheet
	closeButton := newRow("Close", nil, styleOutline, func() { dialog.Hide() })
	dialog = newSheet(remote.window.Canvas(), container.NewBorder(nil, padded(10, 0, 0, 0, container.NewHBox(layout.NewSpacer(), closeButton)), nil, nil, view), size.Width*0.72)
	dialog.Show()
}

func (remote *remoteDesktop) renderTasks() {
	if remote.tabViews["todo"] == nil || remote.todoTasks == nil {
		return
	}
	signature, _ := json.Marshal([]any{remote.meta.Todo, remote.meta.Tasks})
	if string(signature) != remote.todoSignature {
		remote.todoSignature = string(signature)
		remote.todoTasks.RemoveAll()
		remote.todoRows = map[string]*widget.Label{}
		if len(remote.meta.Todo) == 0 {
			remote.todoTasks.Add(note("No tasks yet."))
		}
		for _, task := range remote.meta.Todo {
			label := note("")
			remote.todoRows[task.ID] = label
			remote.todoTasks.Add(label)
		}
		// The server's background commands, newest first, each with its own Stop.
		if len(remote.meta.Tasks) > 0 {
			remote.todoTasks.Add(sectionTitle("Background"))
		}
		for i := len(remote.meta.Tasks) - 1; i >= 0; i-- {
			task := remote.meta.Tasks[i]
			state := "running since " + task.Started.Format("15:04")
			switch {
			case task.Running:
			case task.Stopped:
				state = "stopped"
			case task.ExitCode == 0:
				state = "done in " + task.Ended.Sub(task.Started).Round(time.Second).String()
			default:
				state = fmt.Sprintf("failed with code %d", task.ExitCode)
			}
			var stop fyne.CanvasObject = layout.NewSpacer()
			if task.Running {
				path := "/api/chats/" + remote.meta.Chat + "/tasks/" + task.ID + "/stop"
				stop = newRow("Stop", iconStop, styleChip, func() { remote.action(path, map[string]any{}) })
			}
			remote.todoTasks.Add(container.NewBorder(nil, nil, nil, stop, note("$ "+task.Command+" · "+state)))
		}
	}
	for _, task := range remote.meta.Todo {
		item := agent.TodoItem{ID: task.ID, Text: task.Text, Status: task.Status}
		if task.Started > 0 {
			value := time.UnixMilli(task.Started)
			item.StartedAt = &value
		}
		if task.Completed > 0 {
			value := time.UnixMilli(task.Completed)
			item.CompletedAt = &value
		}
		if label := remote.todoRows[task.ID]; label != nil {
			label.SetText(task.Status + " · " + task.Text + " · " + taskElapsed(item, time.Now()))
		}
	}
	remote.todoFooter.SetText(remote.meta.ContextStatus)
}
