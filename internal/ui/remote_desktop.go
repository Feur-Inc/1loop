package ui

// The desktop as a client of a headless server (docs/SERVER.md): connecting and
// pairing, then the server as the window's execution target, as on the phone.
// Its projects and chats take the sidebar's place, and they open, run and
// show in the same conversation, composer and side panel as this computer's.

import (
	"bufio"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
	"1loop/internal/update"
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
					fyne.Do(func() { sheet.dismiss(); ui.useServer(client) })
					return
				}
			}
			var reply struct {
				Pairing, Code  string
				Console, Phone bool
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
				ui.confirmServerPair(client, reply.Pairing, reply.Code, reply.Phone)
			})
		}()
	}
	sheet = ui.modal("Connect to a remote server", container.NewVBox(note("The server runs your model, browser and terminal on its machine. For a private certificate, copy its SHA-256 fingerprint from the VM console before connecting."), widget.NewLabel("HTTPS address"), address, widget.NewLabel("Certificate fingerprint"), pin), 620, nil, connect)
}

func (ui *desktopUI) confirmServerPair(client *remoteClient, pairingID, code string, phone bool) {
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
				ui.useServer(client)
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
	if phone { // a phone shared on the network: its user types the code in 1loop's request
		body = container.NewVBox(note("On the phone, type this code in 1loop's request, then tap Allow. The pairing expires after two minutes."), codeBlock(code, 80), status)
	}
	sheet = ui.modal("Approve this desktop on the server", body, 620, nil, confirm)
}

// remoteDesktop is a server in use: the window's execution target.
type remoteDesktop struct {
	parent           *desktopUI
	client           *remoteClient
	ctx              context.Context
	cancel           context.CancelFunc
	window           fyne.Window
	status           interface{ SetText(string) } // the line beside the target, under the conversation
	wake             chan struct{}
	name             string // as the server calls itself
	defaultWorkspace string
	current          string // the open chat's ID on the server, "" on the start screen
	chats            []remoteapi.Chat
	projects         []string
	worktrees        map[string]string // the server's worktrees: folder → project
	meta             remoteapi.Meta
	models           []remoteapi.Model
	model, effort    string // the server's defaults, for a new chat
	approvalMode     string
	autoMode         bool
	convs            map[string]*conversation // the server's chats as this window shows them
	fileAttachments  []agent.FileAttachment
	update           *update.Status // the server runs an older version (remote_desktop_update.go)
	approvalID       string
	approvalSheet    *sheet
	questionID       string
	questionSheet    *sheet
	// The open chat's server tabs (browser, terminal, tasks, files).
	tabViews      map[string]fyne.CanvasObject
	tabCancel     map[string]context.CancelFunc
	tabActivate   map[string]func(bool)
	hiddenTabs    map[string]bool
	asked         map[string]bool // kinds the user just asked for: shown when they come
	todoTasks     *fyne.Container
	todoFooter    *widget.Label
	todoRows      map[string]*widget.Label
	todoSignature string
}

// useServer makes a server the window's target: the sidebar lists its
// projects and chats, and what is sent runs there.
func (ui *desktopUI) useServer(client *remoteClient) {
	if ui.server != nil {
		ui.server.close()
	} else {
		ui.localCfg = ui.cfg
		if ui.localCfg.MachineID != "" { // back from the server, the target is this computer
			ui.localCfg.MachineID, ui.localCfg.Workspace = "", ui.lastLocalWorkspace()
		}
	}
	ctx, cancel := context.WithCancel(ui.ctx)
	remote := &remoteDesktop{
		parent: ui, client: client, ctx: ctx, cancel: cancel, window: ui.window, status: ui.serverStatus,
		wake: make(chan struct{}, 1), convs: map[string]*conversation{},
	}
	remote.resetTabs()
	ui.server = remote
	ui.machine.SetText(serverName(client.address.URL))
	ui.serverStatus.SetText("Connecting…")
	ui.serverStatus.Show()
	ui.cfg = agent.Config{} // what the server says replaces it (refreshLoop)
	ui.fileAttachments = nil
	ui.renderAttachments()
	ui.startNew()
	go remote.refreshLoop()
	go remote.events()
	go func() { // the tasks' clocks
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
	remote.checkServerUpdate()
	remote.notify()
}

// useComputer makes this computer the target again.
func (ui *desktopUI) useComputer() {
	if ui.server == nil {
		if ui.cfg.MachineID != "" {
			ui.leaveMachine()
		}
		return
	}
	ui.server.close()
	ui.server = nil
	ui.cfg = ui.localCfg
	ui.machine.SetText("This computer")
	ui.serverStatus.SetText("")
	ui.serverStatus.Hide()
	ui.startNew()
	ui.showChatModel()
	ui.updateApprovalControl()
}

// serverName is how the target menu names a server: its host.
func serverName(address string) string {
	if parsed, err := url.Parse(address); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return address
}

func (remote *remoteDesktop) close() {
	remote.closeTabs()
	remote.cancel()
	if remote.approvalSheet != nil {
		remote.approvalSheet.dismiss()
	}
	if remote.questionSheet != nil {
		remote.questionSheet.dismiss()
	}
	for _, conv := range remote.convs {
		conv.disposePanel()
	}
}

// targetMenu chooses where the agent works, as on the phone: this
// computer, the server last used, or another one.
func (ui *desktopUI) targetMenu() {
	var saved serverAddress
	if data, err := os.ReadFile(remoteAddressPath()); err == nil {
		json.Unmarshal(data, &saved)
	}
	computer := menuAction{label: "This computer", icon: theme.ComputerIcon(), checked: ui.server == nil && ui.cfg.MachineID == "", run: ui.useComputer}
	actions := []menuAction{computer}
	for _, machine := range ui.sshMachines {
		id := machine.ID
		actions = append(actions, menuAction{label: machine.Label(), detail: "SSH · " + machine.User + "@" + machine.Host, icon: iconTerminal,
			checked: ui.server == nil && ui.cfg.MachineID == id, run: func() { ui.useMachine(id) }})
	}
	actions = append(actions, menuAction{label: "Add an SSH machine…", icon: iconPlus, run: func() { ui.prefs.openOn("SSH machines") }})
	if saved.URL != "" && saved.Token != "" {
		server := menuAction{label: serverName(saved.URL), detail: saved.URL, icon: iconBrowser, checked: ui.server != nil && ui.server.client.address.URL == saved.URL, run: func() {
			if ui.server != nil && ui.server.client.address.URL == saved.URL {
				return
			}
			client, err := newRemoteClient(saved)
			if err != nil {
				ui.notice("Remote server", err.Error())
				return
			}
			ui.useServer(client)
		}}
		actions = append(actions, server)
	}
	actions = append(actions, menuAction{label: "Connect a server…", icon: iconPlus, run: ui.connectServer})
	if remote := ui.server; remote != nil {
		actions = append(actions,
			menuAction{label: "Server hooks…", icon: iconTerminal, run: remote.editHooks},
			menuAction{label: "Copy a project to the server…", icon: iconFolder, run: remote.copyProject})
		if offer := remote.update; offer != nil && offer.Available {
			label, run := "Update the server to "+offer.Latest, remote.updateServer
			if !offer.CanUpdate {
				label, run = "Server "+offer.Current+" · "+offer.Latest+" is out", func() { remote.status.SetText(offer.Problem) }
			}
			actions = append(actions, menuAction{label: label, icon: iconArrowTurn, run: run})
		}
	}
	showMenu(ui.window, ui.machine, actions)
}

func (remote *remoteDesktop) notify() {
	select {
	case remote.wake <- struct{}{}:
	default:
	}
}

func (remote *remoteDesktop) draftKey(chat string) string {
	if chat == "" {
		return "server:" + remote.client.address.URL + "|" + newChatDraft
	}
	return "server:" + remote.client.address.URL + "|" + chat
}

// conv is a server chat as the window shows it.
func (remote *remoteDesktop) conv(id string) *conversation {
	conv := remote.convs[id]
	if conv == nil {
		conv = &conversation{ID: remote.draftKey(id), server: remote, serverID: id, states: map[int]string{}}
		remote.convs[id] = conv
	}
	for _, chat := range remote.chats {
		if chat.ID == id {
			conv.Title, conv.Config.Workspace = chat.Title, chat.Workspace
		}
	}
	return conv
}

// openChat shows a server chat; its items come with the next refresh.
func (remote *remoteDesktop) openChat(id string) {
	if remote.current == id && remote.parent.current != nil && remote.parent.current.server == remote {
		return
	}
	remote.selectChat(id)
	conv := remote.conv(id)
	remote.parent.openChat(conv)
	if conv.Config.Model != "" {
		remote.parent.model.row.SetText(remote.modelText(conv.Config.Model, conv.Config.Effort))
	}
}

// selectChat follows the chat the window shows: its draft, tabs and dialogs.
func (remote *remoteDesktop) selectChat(id string) {
	remote.current = id
	remote.fileAttachments = nil
	remote.meta = remoteapi.Meta{}
	remote.closeTabs()
	remote.notify()
}

func (remote *remoteDesktop) resetTabs() {
	remote.tabViews = map[string]fyne.CanvasObject{}
	remote.tabCancel = map[string]context.CancelFunc{}
	remote.tabActivate = map[string]func(bool){}
	remote.hiddenTabs = map[string]bool{}
	remote.asked = map[string]bool{}
}

// closeTabs ends the streams of the server tabs and takes them out of the
// panel: the next refresh brings back those of the chat on screen. They stay
// open on the server.
func (remote *remoteDesktop) closeTabs() {
	for _, cancel := range remote.tabCancel {
		cancel()
	}
	panel := remote.parent.panel
	if owner := panel.owner; owner != nil && owner.server == remote {
		panel.tabs, panel.active = withoutServerTabs(panel.tabs, panel.active)
	}
	for _, conv := range remote.convs {
		if conv.panel != nil {
			conv.panel.tabs, conv.panel.active = withoutServerTabs(conv.panel.tabs, conv.panel.active)
		}
	}
	remote.resetTabs()
}

// withoutServerTabs is a panel's tabs but the server's, and its active tab.
func withoutServerTabs(tabs []panelTab, active int) ([]panelTab, int) {
	var kept []panelTab
	for i, tab := range tabs {
		if strings.HasPrefix(tab.kind, "server:") {
			if i < active {
				active--
			} else if i == active {
				active = -1
			}
			continue
		}
		kept = append(kept, tab)
	}
	if active < 0 && len(kept) > 0 {
		active = len(kept) - 1
	}
	return kept, active
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

// submit sends what the composer holds to the server: Enter, or Ctrl+Enter
// ("steer") while the chat works.
func (remote *remoteDesktop) submit(mode string) {
	ui := remote.parent
	text := ui.prompt.Text
	if strings.TrimSpace(text) == "" && len(remote.fileAttachments) == 0 {
		return
	}
	chat := remote.current
	pending := "sending:" + core.Identifier() + "|" + remote.draftKey(chat)
	ui.prompt.drafts.set(pending, text)
	ui.prompt.clearSent()
	files := append([]agent.FileAttachment(nil), remote.fileAttachments...)
	remote.fileAttachments = nil
	remote.post(chat, text, mode, nil, files, pending)
}

// post sends a message; a handoff the server asks for is answered first.
func (remote *remoteDesktop) post(chat, text, mode string, handoff *agent.HandoffChoice, files []agent.FileAttachment, pending string) {
	ui := remote.parent
	workspace := ui.cfg.Workspace
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
		defer cancel()
		var reply struct{ Chat string }
		body := map[string]any{"chat": chat, "text": text, "mode": mode, "attachments": files}
		if chat == "" && workspace != "" {
			body["workspace"] = workspace
		}
		if handoff != nil {
			body["handoff"] = handoff
		}
		err := remote.client.json(ctx, "POST", "/api/send", body, &reply)
		fyne.Do(func() {
			if remote.ctx.Err() != nil {
				return
			}
			restore := func() {
				key := remote.draftKey(chat)
				if remote.current == chat {
					ui.prompt.restoreDraft(text)
				} else {
					current := ui.prompt.drafts.get(key)
					if current != "" && current != text {
						text = current + "\n\n" + text
					}
					ui.prompt.drafts.set(key, text)
				}
				ui.prompt.drafts.set(pending, "")
				if remote.current == chat {
					remote.fileAttachments = append(files, remote.fileAttachments...)
				}
			}
			if required, ok := err.(handoffRequired); ok {
				showHandoffDialog(remote.window, required.plan, func(choice agent.HandoffChoice) {
					remote.post(chat, text, mode, &choice, files, pending)
				}, restore)
				return
			}
			if err != nil {
				remote.status.SetText(err.Error())
				restore()
				return
			}
			ui.prompt.drafts.set(pending, "")
			if chat == "" && reply.Chat != "" && remote.current == "" && ui.current == nil {
				// The server lists the chat with the next refresh: its folder is
				// the one it was sent to, not the server's default.
				remote.conv(reply.Chat).Config.Workspace = workspace
				remote.openChat(reply.Chat)
			}
			remote.notify()
		})
	}()
}

func (remote *remoteDesktop) stop() {
	remote.action("/api/stop", map[string]string{"chat": remote.current})
}

func (remote *remoteDesktop) addTab(kind string) {
	if remote.current == "" {
		remote.status.SetText("Start a conversation before opening server tools.")
		return
	}
	delete(remote.hiddenTabs, kind)
	remote.asked[kind] = true
	remote.action("/api/chats/"+remote.current+"/panel", map[string]string{"action": "open", "kind": kind})
}

// panelMenu is the side panel's + for a server chat: the server's tools.
func (remote *remoteDesktop) panelMenu(anchor fyne.CanvasObject) {
	showMenu(remote.window, anchor, []menuAction{
		{label: "Tasks", icon: iconCheck, run: func() { remote.showTab("todo") }},
		{label: "Git changes", icon: toolGlyph("edit_file"), run: func() { remote.showTab("changes") }},
		{label: "Browser", icon: iconBrowser, run: func() { remote.addTab("browser") }},
		{label: "Terminal", icon: toolGlyph("shell"), run: func() { remote.addTab("terminal") }},
		{label: "Files", icon: iconFolder, run: remote.openFiles},
	})
}

// showTab shows a tab the server always has (its tasks, its changes).
func (remote *remoteDesktop) showTab(key string) {
	panel := remote.parent.panel
	for i, tab := range panel.tabs {
		if tab.kind == "server:"+key {
			panel.choose(i)
			panel.unfold()
			return
		}
	}
	delete(remote.hiddenTabs, key)
	remote.asked[key] = true
	remote.notify()
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

// refreshLoop reads the server's state and the open chat after each event
// (at most every 100 ms), and shows them.
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
			Worktrees       map[string]string
			Settings        struct {
				Models                      []remoteapi.Model
				Model, Effort, ApprovalMode string
				AutoMode                    bool // the server's model has an Auto mode
			}
		}
		err := remote.client.json(ctx, "GET", "/api/state", nil, &state)
		var projects struct{ Projects []string }
		if err == nil {
			remote.client.json(ctx, "GET", "/api/projects", nil, &projects) // an older server has none to list
		}
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
			if remote.current != id || remote.ctx.Err() != nil || remote.parent.server != remote {
				return
			}
			if err != nil {
				remote.status.SetText(err.Error())
				return
			}
			remote.worktrees = state.Worktrees
			remote.show(state.Name, state.Workspace, state.Chats, projects.Projects)
			remote.models = state.Settings.Models
			remote.model, remote.effort = state.Settings.Model, state.Settings.Effort
			remote.approvalMode, remote.autoMode = state.Settings.ApprovalMode, state.Settings.AutoMode
			remote.parent.updateApprovalControl()
			if id == "" {
				remote.parent.model.row.SetText(remote.modelText(remote.model, remote.effort))
				return
			}
			remote.meta = chat.Meta
			conv := remote.conv(id)
			conv.Config.Model, conv.Config.Effort = chat.Meta.Model, chat.Meta.Effort // shown at once when it opens again
			remote.parent.model.row.SetText(remote.modelText(chat.Meta.Model, chat.Meta.Effort))
			remote.showItems(conv, chat.Items)
			remote.showMeta(conv)
			remote.syncTabs(conv, tabs.Tabs)
		})
	}
}

// show updates the server's name, projects and chats in the sidebar.
func (remote *remoteDesktop) show(name, workspace string, chats []remoteapi.Chat, listed []string) {
	ui := remote.parent
	remote.name, remote.defaultWorkspace = name, workspace
	projects := []string{}
	add := func(path string) {
		if path != "" && !slices.Contains(projects, path) && remote.worktrees[path] == "" {
			projects = append(projects, path)
		}
	}
	add(workspace)
	for _, path := range listed {
		add(path)
	}
	for _, chat := range chats {
		add(chat.Workspace)
	}
	if ui.cfg.Workspace == "" {
		ui.cfg.Workspace = workspace
		ui.updateHeader()
	}
	status := "Connected to " + name
	if remote.meta.Running && remote.current != "" {
		status += " · " + remote.meta.Label
	}
	remote.status.SetText(status)
	if slices.Equal(remote.chats, chats) && slices.Equal(remote.projects, projects) {
		return
	}
	remote.chats, remote.projects = chats, projects
	for _, chat := range chats {
		if conv := remote.convs[chat.ID]; conv != nil && conv.Title != chat.Title {
			conv.Title = chat.Title
			if conv == ui.current {
				ui.updateHeader()
			}
		}
	}
	ui.renderChats()
}

// renderChats is the sidebar for a server: its projects, and the chats of
// the selected one.
func (remote *remoteDesktop) renderChats() {
	ui := remote.parent
	ui.projectList.RemoveAll()
	opened := ui.projectOf(ui.cfg.Workspace)
	for _, p := range remote.projects {
		p := p
		row := newRow(path.Base(filepath.ToSlash(p)), iconFolder, styleNav, func() { remote.selectProject(p) })
		row.selected = p == ui.cfg.Workspace
		row.accent = true
		row.truncate = true
		if state := ui.repoFor(p); state.repo != nil {
			row.trailing = state.repo.Current
		}
		ui.projectList.Add(withMore(row, func(anchor fyne.CanvasObject) *sheet {
			actions := []menuAction{{label: "New chat", icon: iconPlus, run: func() { remote.selectProject(p); ui.startNew() }}}
			return showMenu(ui.window, anchor, append(actions, ui.repositoryActions(p)...))
		}))
		if p == opened {
			for _, object := range ui.worktreeRows(p, remote.selectProject) {
				ui.projectList.Add(object)
			}
			ui.loadRepo(p, true, 20*time.Second)
		}
	}
	ui.chatList.RemoveAll()
	shown := 0
	for _, chat := range remote.chats {
		if chat.Workspace != ui.cfg.Workspace && ui.cfg.Workspace != "" {
			continue
		}
		chat := chat
		shown++
		row := newRow(chat.Title, nil, styleNav, func() { remote.openChat(chat.ID) })
		row.accent = true
		row.selected = ui.current != nil && ui.current.server == remote && ui.current.serverID == chat.ID
		if chat.Running {
			row.badge = iconDot // the agent works on it on the server
		}
		ui.chatList.Add(withMore(row, func(anchor fyne.CanvasObject) *sheet {
			return showMenu(ui.window, anchor, []menuAction{
				{label: "Delete", icon: iconTrash, danger: true, run: func() { remote.deleteChat(chat) }},
			})
		}))
	}
	if shown == 0 {
		ui.chatList.Add(padded(4, 0, 10, 0, textLine("No chats yet", 13, colFaint)))
	}
	if ui.sideLists != nil {
		ui.sideLists.Refresh()
	}
}

// projectMenu is the composer's folder chip for a server: its projects.
func (remote *remoteDesktop) projectMenu(anchor fyne.CanvasObject) {
	ui := remote.parent
	var actions []menuAction
	for _, p := range remote.projects {
		p := p
		actions = append(actions, menuAction{label: path.Base(filepath.ToSlash(p)), detail: p, checked: p == ui.cfg.Workspace, run: func() { remote.selectProject(p) }})
		actions = append(actions, ui.worktreeChoices(p, remote.selectProject)...)
	}
	if len(actions) == 0 {
		actions = append(actions, menuAction{label: "The server has no project yet"})
	}
	showMenu(ui.window, anchor, actions)
}

func (remote *remoteDesktop) selectProject(path string) {
	ui := remote.parent
	if path == ui.cfg.Workspace && ui.current == nil {
		return
	}
	ui.cfg.Workspace = path
	ui.current = nil
	ui.startNew()
}

func (remote *remoteDesktop) deleteChat(chat remoteapi.Chat) {
	ui := remote.parent
	ui.confirm("Delete chat?", fmt.Sprintf("“%s” will be permanently deleted from the server.", chat.Title), "Delete", func() {
		if remote.current == chat.ID {
			ui.startNew()
		}
		if conv := remote.convs[chat.ID]; conv != nil {
			conv.disposePanel()
			delete(remote.convs, chat.ID)
		}
		remote.action("/api/chats/"+chat.ID+"/delete", map[string]any{})
	})
}

// chatItem is a server item as this window's conversation holds it.
func chatItem(item remoteapi.Item) agent.ChatItem {
	converted := agent.ChatItem{ID: item.ID, Kind: item.Kind, Text: item.Text, Tool: item.Tool, Args: item.Args,
		Diff: item.Diff, Millis: item.Millis, Failed: item.Failed}
	for _, name := range item.Files {
		converted.Files = append(converted.Files, agent.FileAttachment{Name: name})
	}
	return converted
}

// showItems brings the open chat's conversation to the server's: a growing
// answer or a tool call changes in place, new items are added, and anything
// else (a new version, an edit) draws the chat again.
func (remote *remoteDesktop) showItems(conv *conversation, items []remoteapi.Item) {
	ui := remote.parent
	if ui.current != conv {
		return
	}
	next := make([]agent.ChatItem, len(items))
	states := map[int]string{}
	versions := map[string]remoteapi.Versions{}
	for i, item := range items {
		next[i] = chatItem(item)
		if item.Versions != nil {
			versions[item.Versions.Anchor] = *item.Versions
		}
		if item.Images > 0 && i < len(conv.Items) && len(conv.Items[i].Images) > 0 && conv.Items[i].ID == item.ID {
			next[i].Images = conv.Items[i].Images // fetched already
		}
		if item.Kind == "tool" {
			states[i] = item.State
		}
	}
	remote.fetchImages(conv, items)
	changed := len(next) < len(conv.Items) || !maps.Equal(versions, conv.versions)
	for i := 0; i < len(conv.Items) && i < len(next) && !changed; i++ {
		old := conv.Items[i]
		switch {
		case reflect.DeepEqual(old, next[i]) && conv.states[i] == states[i]:
		case old.Kind == next[i].Kind && old.ID == next[i].ID && (old.Kind == "assistant" || old.Kind == "tool"):
			conv.Items[i], conv.states[i] = next[i], states[i]
			switch view := ui.liveView(conv, i).(type) {
			case *markdownView:
				view.ParseMarkdown(next[i].Text)
				ui.items.changed(view.box)
			case *toolRow:
				view.update(next[i], states[i])
				if view.preview != nil {
					ui.items.changed(view)
					ui.relayoutColumn()
				}
				ui.panel.changed(conv, i)
			}
		default:
			changed = true
		}
	}
	if changed {
		conv.Items, conv.states, conv.versions = next, states, versions
		ui.reloadChat(conv)
		return
	}
	added := len(next) > len(conv.Items)
	for i := len(conv.Items); i < len(next); i++ {
		conv.Items = append(conv.Items, next[i])
		conv.states[i] = states[i]
		ui.appendItem(conv, i)
	}
	if added {
		ui.syncCopies()
	}
	ui.relayoutColumn()
}

// fetchImages loads attached and generated pictures, which the server
// lists without their data, then shows the chat again with them.
func (remote *remoteDesktop) fetchImages(conv *conversation, items []remoteapi.Item) {
	for index, item := range items {
		if (item.Kind != "user" && item.Tool != "imagegen") || item.Images == 0 || (index < len(conv.Items) && len(conv.Items[index].Images) > 0) {
			continue
		}
		if conv.fetching == nil {
			conv.fetching = map[string]bool{}
		}
		if conv.fetching[item.ID] {
			continue
		}
		conv.fetching[item.ID] = true
		index, item, chat := index, item, conv.serverID
		go func() {
			var parts []core.ImagePart
			for n := 0; n < item.Images; n++ {
				ctx, cancel := context.WithTimeout(remote.ctx, 30*time.Second)
				response, err := remote.client.request(ctx, "GET", fmt.Sprintf("/api/chats/%s/items/%d/images/%d", chat, index, n), nil)
				if err == nil {
					data, readErr := io.ReadAll(io.LimitReader(response.Body, 32<<20))
					response.Body.Close()
					if readErr == nil {
						parts = append(parts, core.ImagePart{Mime: response.Header.Get("Content-Type"), Data: base64.StdEncoding.EncodeToString(data)})
					}
				}
				cancel()
			}
			fyne.Do(func() {
				if len(parts) == 0 || index >= len(conv.Items) || conv.Items[index].ID != item.ID {
					return
				}
				conv.Items[index].Images = parts
				if remote.parent.current == conv {
					remote.parent.reloadChat(conv)
				}
			})
		}()
	}
}

// showMeta shows whether the open chat works, and what waits for the user.
func (remote *remoteDesktop) showMeta(conv *conversation) {
	ui := remote.parent
	meta := remote.meta
	ui.syncRun()
	ui.setContextUsage(meta.Used, meta.Window)
	approvalID := ""
	if meta.Approval != nil {
		approvalID = meta.Approval.ID
	}
	if approvalID != remote.approvalID {
		if remote.approvalSheet != nil {
			remote.approvalSheet.dismiss()
			remote.approvalSheet = nil
		}
		remote.approvalID = approvalID
		if approval := meta.Approval; approval != nil {
			remote.approvalSheet = ui.showApproval(conv, map[string]any{"id": approval.ID, "title": approval.Title, "preview": approval.Preview})
		}
	}
	questionID := ""
	if meta.Question != nil {
		questionID = meta.Question.ID
	}
	if questionID != remote.questionID {
		if remote.questionSheet != nil {
			remote.questionSheet.dismiss()
			remote.questionSheet = nil
		}
		remote.questionID = questionID
		if question := meta.Question; question != nil {
			request := agent.QuestionRequest{ID: question.ID}
			for _, q := range question.Questions {
				asked := agent.Question{ID: q.ID, Header: q.Header, Question: q.Question}
				for _, option := range q.Options {
					asked.Options = append(asked.Options, agent.QuestionOption{Label: option.Label, Description: option.Description})
				}
				request.Questions = append(request.Questions, asked)
			}
			remote.questionSheet = ui.showQuestions(conv, request)
		}
	}
}

// syncRun is ui.syncRun for a server chat: Stop and the activity line while
// the server's agent works.
func (remote *remoteDesktop) syncRun() {
	ui := remote.parent
	if remote.meta.Running {
		ui.mark.stop()
		ui.send.Hide()
		ui.stop.Show()
		ui.showActivityLine(true)
		ui.workMark.setState(cmp.Or(remote.meta.Mark, "wait"))
		ui.thinkLabel.SetText(remote.meta.Label)
	} else {
		ui.send.Show()
		ui.stop.Hide()
		ui.showActivityLine(false)
		ui.workMark.setState("brand")
	}
}

// modelText names the chat's model on the composer's chip.
func (remote *remoteDesktop) modelText(label, effort string) string {
	text := label
	for _, model := range remote.models {
		if model.Label == label {
			text = model.Name
			for _, entry := range model.Efforts {
				if entry.Value == effort {
					text += " · " + entry.Label
				}
			}
		}
	}
	if text == "" {
		return "Model"
	}
	return text
}

// modelMenu chooses the chat's model and reasoning among the server's.
func (remote *remoteDesktop) modelMenu() {
	ui := remote.parent
	current, effort := remote.model, remote.effort
	if remote.current != "" {
		current, effort = remote.meta.Model, remote.meta.Effort
	}
	var actions []menuAction
	for _, model := range remote.models {
		model := model
		action := menuAction{label: model.Name, detail: model.Detail, run: func() {
			remote.action("/api/model", map[string]string{"chat": remote.current, "label": model.Label})
		}}
		action.checked = model.Label == current
		actions = append(actions, action)
	}
	for _, model := range remote.models {
		if model.Label != current {
			continue
		}
		for _, entry := range model.Efforts {
			entry := entry
			action := menuAction{label: "Reasoning · " + entry.Label, run: func() {
				remote.action("/api/model", map[string]string{"chat": remote.current, "effort": entry.Value})
			}}
			action.checked = entry.Value == effort
			actions = append(actions, action)
		}
	}
	if len(actions) == 0 {
		actions = append(actions, menuAction{label: "The server has no model yet"})
	}
	showMenu(ui.window, ui.model.row, actions)
}

// approvalMenu is the approval chip for a server: its mode, for every chat.
func (remote *remoteDesktop) approvalMenu() {
	ui := remote.parent
	modes := []string{"approval", "yolo"}
	if remote.autoMode {
		modes = append(modes, "auto")
	}
	names := map[string]string{"approval": "Approval required", "yolo": "YOLO", "auto": "Auto"}
	var actions []menuAction
	for _, mode := range modes {
		mode := mode
		action := menuAction{label: names[mode], run: func() { remote.action("/api/approval-mode", map[string]string{"mode": mode}) }}
		action.checked = mode == remote.approvalMode
		actions = append(actions, action)
	}
	showMenu(ui.window, ui.approvalControl, actions)
}

// syncTabs keeps the open chat's panel on the server's tabs: its browser
// and terminals, its task list, its changes.
func (remote *remoteDesktop) syncTabs(conv *conversation, tabs []remoteapi.Tab) {
	panel := remote.parent.panel
	if panel.owner != conv {
		return
	}
	present := map[string]bool{"files": true} // the Files tab is this window's own
	for _, tab := range tabs {
		present[tab.Key] = true
	}
	for key, cancel := range remote.tabCancel {
		if present[key] {
			continue
		}
		// Closed on the server: gone from the panel, without asking the
		// server to close it again.
		cancel()
		delete(remote.tabViews, key)
		delete(remote.tabCancel, key)
		delete(remote.tabActivate, key)
		for i, tab := range panel.tabs {
			if tab.kind == "server:"+key {
				panel.tabs = append(panel.tabs[:i], panel.tabs[i+1:]...)
				if panel.active >= len(panel.tabs) || panel.active > i {
					panel.active--
				}
				panel.render()
				break
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
		show := remote.asked[tab.Kind] || remote.asked[tab.Key]
		delete(remote.asked, tab.Kind)
		delete(remote.asked, tab.Key)
		remote.addPanelTab(tab.Key, tab.Title, tab.Kind, content, cancel, activate, show)
	}
	remote.renderTasks()
}

// addPanelTab puts a server tab in the panel; closing it closes it on the
// server too (a browser or a terminal), or hides it (the others).
func (remote *remoteDesktop) addPanelTab(key, title, kind string, content fyne.CanvasObject, cancel context.CancelFunc, activate func(bool), show bool) {
	panel := remote.parent.panel
	remote.tabViews[key], remote.tabCancel[key], remote.tabActivate[key] = content, cancel, activate
	icons := map[string]fyne.Resource{"browser": iconBrowser, "terminal": toolGlyph("shell"), "todo": iconCheck, "changes": toolGlyph("edit_file"), "files": iconFolder}
	chat := remote.current
	panel.tabs = append(panel.tabs, panelTab{kind: "server:" + key, title: title, icon: icons[kind], content: content, cancel: func() {
		cancel()
		delete(remote.tabViews, key)
		delete(remote.tabCancel, key)
		delete(remote.tabActivate, key)
		if remote.ctx.Err() != nil || remote.current != chat {
			return
		}
		remote.hiddenTabs[key] = true
		if strings.HasPrefix(key, "browser-") || strings.HasPrefix(key, "terminal-") {
			remote.action("/api/chats/"+chat+"/panel", map[string]string{"action": "close", "key": key})
		}
	}})
	if !show {
		panel.renderStrip()
		return
	}
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
}

// activateTabs tells the server tabs which one is in view (a browser
// streams its screen only while seen).
func (remote *remoteDesktop) activateTabs() {
	panel := remote.parent.panel
	for key, activate := range remote.tabActivate {
		activate(panel.shown > 0 && panel.active >= 0 && panel.active < len(panel.tabs) && panel.tabs[panel.active].kind == "server:"+key)
	}
}

func (remote *remoteDesktop) editHooks() {
	go func() {
		ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
		defer cancel()
		var reply struct{ Content string }
		err := remote.client.json(ctx, "GET", "/api/hooks", nil, &reply)
		fyne.Do(func() {
			if err != nil {
				remote.status.SetText("Hooks: " + err.Error())
				return
			}
			editor := widget.NewMultiLineEntry()
			editor.TextStyle = fyne.TextStyle{Monospace: true}
			editor.Wrapping = fyne.TextWrapOff
			editor.SetText(reply.Content)
			problem := note("")
			scroll := container.NewScroll(editor)
			scroll.SetMinSize(fyne.NewSize(560, 360))
			var dialog *sheet
			cancelButton := newRow("Cancel", nil, styleOutline, func() { dialog.Hide() })
			save := newRow("Save", nil, stylePrimary, nil)
			save.onTap = func() {
				save.Disable()
				content := editor.Text
				go func() {
					ctx, cancel := context.WithTimeout(remote.ctx, 15*time.Second)
					defer cancel()
					err := remote.client.json(ctx, "POST", "/api/hooks", map[string]string{"content": content}, nil)
					fyne.Do(func() {
						save.Enable()
						if err != nil {
							problem.SetText(err.Error())
							return
						}
						dialog.Hide()
					})
				}()
			}
			buttons := container.NewHBox(layout.NewSpacer(), cancelButton, save)
			body := container.NewBorder(note("Hooks of the server: commands before or after its tools, when a message is sent, or when its agent stops."), container.NewVBox(problem, buttons), nil, nil, scroll)
			dialog = newSheet(remote.window.Canvas(), body, 640)
			dialog.Show()
		})
	}()
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
