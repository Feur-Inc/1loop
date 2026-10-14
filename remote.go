package main

// Phone remote: 1loop serves its chats on the local network so the 1loop
// Android app can follow them and drive them. Everything a phone does goes
// through the same functions as the window, so it shows on the computer.
//
// Discovery: the phone broadcasts "1loop?" on UDP remoteDiscoveryPort and
// every 1loop on the network answers with its name and HTTP port.
//
// Pairing: a phone first asks to pair; 1loop shows a six digit code on the
// computer, the user types it on the phone, which then gets a token. Only a
// hash of the token is kept. Every other request carries the token: the
// agent runs commands on this computer, so nobody on the network may drive
// it without the user's consent.
//
// API (JSON over HTTP):
//   GET  /api/info                      name and version, no token needed
//   POST /api/pair       {device}       → {pairing}   (the code shows on screen)
//   POST /api/pair/confirm {pairing,code} → {token}
//   GET  /api/state                     projects, chats, settings
//   GET  /api/chats/{id}                one chat with its items
//   GET  /api/events                    server-sent events (see remoteEvent)
//   POST /api/send       {chat,text,mode,workspace}  mode: send, queue or steer
//   POST /api/stop       {chat}
//   POST /api/approve    {id,allow}
//   POST /api/model      {chat,label,effort}    model and reasoning effort
//   POST /api/approval-mode {mode}              "approval" or "yolo"
//   /api/chats/{id}/panel…                      the chat's side panel (remote_panel.go)

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

const (
	remoteDiscoveryPort = 47100
	remoteHTTPPort      = 47101
	remoteProtocol      = 1
)

// pairedDevice is a phone allowed to drive 1loop; only its token's hash is kept.
type pairedDevice struct {
	Name      string    `json:"name"`
	TokenHash string    `json:"tokenHash"`
	Added     time.Time `json:"added"`
}

type pairing struct {
	device  string
	code    string
	tries   int
	expires time.Time
	sheet   *sheet
}

type remoteEvent struct {
	kind string
	data []byte
}

type remoteServer struct {
	ui    *desktopUI
	name  string
	port  int
	http  *http.Server
	udp   *net.UDPConn
	mu    sync.Mutex
	subs  map[chan remoteEvent]bool
	pairs map[string]*pairing

	// Changes waiting for the next flush (UI thread only).
	dirty     map[*conversation]map[int]bool
	meta      map[*conversation]bool
	chatsDirt bool
	settings  bool
	flushing  bool
}

// startRemote opens the server; when the port is taken (another 1loop),
// it picks any free port and still answers discovery when it can.
func (ui *desktopUI) startRemote() {
	if !ui.remoteEnabled {
		return
	}
	host, _ := os.Hostname()
	server := &remoteServer{ui: ui, name: host, subs: map[chan remoteEvent]bool{}, pairs: map[string]*pairing{},
		dirty: map[*conversation]map[int]bool{}, meta: map[*conversation]bool{}}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", remoteHTTPPort))
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		return // no network: the window works without its remote
	}
	server.port = ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", server.info)
	mux.HandleFunc("POST /api/pair", server.pair)
	mux.HandleFunc("POST /api/pair/confirm", server.confirmPair)
	mux.HandleFunc("GET /api/state", server.authorized(server.state))
	mux.HandleFunc("GET /api/chats/{id}", server.authorized(server.chat))
	mux.HandleFunc("GET /api/events", server.authorized(server.events))
	mux.HandleFunc("POST /api/send", server.authorized(server.send))
	mux.HandleFunc("POST /api/stop", server.authorized(server.stop))
	mux.HandleFunc("POST /api/approve", server.authorized(server.approve))
	mux.HandleFunc("POST /api/model", server.authorized(server.setModel))
	mux.HandleFunc("POST /api/approval-mode", server.authorized(server.setApprovalMode))
	mux.HandleFunc("GET /api/chats/{id}/panel", server.authorized(server.panelTabs))
	mux.HandleFunc("GET /api/chats/{id}/panel/{key}", server.authorized(server.panelTab))
	mux.HandleFunc("GET /api/chats/{id}/panel/{key}/screen", server.authorized(server.panelScreen))
	mux.HandleFunc("POST /api/chats/{id}/panel/{key}/input", server.authorized(server.panelInput))
	server.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go server.http.Serve(ln)
	if udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: remoteDiscoveryPort}); err == nil {
		server.udp = udp
		go server.answerDiscovery()
	}
	ui.remote = server
}

func (server *remoteServer) close() {
	if server == nil {
		return
	}
	server.http.Close()
	if server.udp != nil {
		server.udp.Close()
	}
}

// answerDiscovery replies to every "1loop?" broadcast with where to connect.
func (server *remoteServer) answerDiscovery() {
	buf := make([]byte, 512)
	for {
		n, from, err := server.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(buf[:n])) != "1loop?" {
			continue
		}
		reply, _ := json.Marshal(map[string]any{"service": "1loop", "name": server.name, "port": server.port, "protocol": remoteProtocol})
		server.udp.WriteToUDP(reply, from)
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers

func writeJSON(writer http.ResponseWriter, status int, v any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	json.NewEncoder(writer).Encode(v)
}

func fail(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func readJSON(req *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(v)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// authorized lets a request through only with a paired device's token.
func (server *remoteServer) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = req.URL.Query().Get("token") // event streams cannot always set headers
		}
		hash := tokenHash(token)
		ok := false
		server.onUI(func() {
			for _, pairedDevice := range server.ui.devices {
				if subtle.ConstantTimeCompare([]byte(pairedDevice.TokenHash), []byte(hash)) == 1 {
					ok = true
				}
			}
		})
		if !ok {
			fail(writer, http.StatusUnauthorized, "This phone is not paired with 1loop")
			return
		}
		next(writer, req)
	}
}

// onUI runs f on the window's thread, where all chat state lives.
func (server *remoteServer) onUI(f func()) {
	if server.ui.ctx.Err() == nil {
		fyne.DoAndWait(f)
	}
}

// ---------------------------------------------------------------------------
// Pairing

func (server *remoteServer) info(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{"service": "1loop", "name": server.name, "protocol": remoteProtocol})
}

func (server *remoteServer) pair(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Device string `json:"device"`
	}
	if readJSON(req, &body) != nil {
		fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	device := strings.TrimSpace(body.Device)
	if device == "" {
		device = "Phone"
	}
	if len([]rune(device)) > 40 {
		device = string([]rune(device)[:40])
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	pairingValue := &pairing{device: device, code: fmt.Sprintf("%06d", n.Int64()), expires: time.Now().Add(2 * time.Minute)}
	id := identifier()
	server.onUI(func() {
		for key, old := range server.pairs { // one pending pairing at a time
			if old.sheet != nil {
				old.sheet.Hide()
			}
			delete(server.pairs, key)
		}
		server.pairs[id] = pairingValue
		pairingValue.sheet = server.ui.showPairing(pairingValue)
	})
	writeJSON(writer, http.StatusOK, map[string]string{"pairing": id})
}

func (server *remoteServer) confirmPair(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Pairing string `json:"pairing"`
		Code    string `json:"code"`
	}
	if readJSON(req, &body) != nil {
		fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	var token, problem string
	server.onUI(func() {
		pairing := server.pairs[body.Pairing]
		switch {
		case pairing == nil || time.Now().After(pairing.expires):
			problem = "This code has expired. Ask for a new one."
		case subtle.ConstantTimeCompare([]byte(pairing.code), []byte(strings.TrimSpace(body.Code))) != 1:
			pairing.tries++
			problem = "Wrong code."
			if pairing.tries >= 5 { // a guessed code would give the shell away
				problem = "Too many wrong codes. Ask for a new one."
				pairing.expires = time.Time{}
			}
		default:
			token = identifier()
			server.ui.devices = append(server.ui.devices, pairedDevice{Name: pairing.device, TokenHash: tokenHash(token), Added: time.Now()})
			server.ui.save()
			if pairing.sheet != nil {
				pairing.sheet.Hide()
			}
			delete(server.pairs, body.Pairing)
		}
		if problem != "" && pairing != nil && pairing.expires.IsZero() && pairing.sheet != nil {
			pairing.sheet.Hide()
		}
	})
	if problem != "" {
		fail(writer, http.StatusForbidden, problem)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"token": token, "name": server.name})
}

// showPairing shows the code a phone must type.
func (ui *desktopUI) showPairing(pairingValue *pairing) *sheet {
	code := textLine(pairingValue.code[:3]+" "+pairingValue.code[3:], 40, colAmber)
	code.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	intro := widget.NewLabel("“" + pairingValue.device + "” wants to control 1loop from the phone. Type this code in the app to allow it. It expires in two minutes.")
	intro.Wrapping = fyne.TextWrapWord
	hint := note("A paired phone can send messages and approve commands. Remove it any time in Settings.")
	body := container.NewVBox(intro, spacer(0, 6), container.NewCenter(code), spacer(0, 6), hint)
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal("Pair a phone", body, 460, func() { pairingValue.expires = time.Time{} }, cancel)
	return dialog
}

// ---------------------------------------------------------------------------
// State as the phone sees it

type remoteChat struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Workspace string `json:"workspace"`
	Running   bool   `json:"running"`
	Items     int    `json:"items"`
}

type remoteItem struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Images int    `json:"images,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Verb   string `json:"verb,omitempty"`
	Target string `json:"target,omitempty"`
	Mono   bool   `json:"mono,omitempty"`
	Meta   string `json:"meta,omitempty"`
	Tone   string `json:"tone,omitempty"` // of the meta: muted, added, danger, amber
	State  string `json:"state,omitempty"`
	Args   string `json:"args,omitempty"`
	Diff   string `json:"diff,omitempty"`
	Millis int64  `json:"millis,omitempty"`
	Failed string `json:"failed,omitempty"`
}

type remoteMeta struct {
	Chat     string          `json:"chat"`
	Title    string          `json:"title"`
	Running  bool            `json:"running"`
	Label    string          `json:"label,omitempty"`
	Mark     string          `json:"mark,omitempty"`
	Queue    []string        `json:"queue"`
	Steer    []string        `json:"steer"`
	Items    int             `json:"items"`
	Approval *remoteApproval `json:"approval,omitempty"`
	// The chat's model, by its label in the model list, and context use.
	Model  string       `json:"model"`
	Effort string       `json:"effort"`
	Used   int          `json:"used"`
	Window int          `json:"window"`
	Todo   []remoteTodo `json:"todo"` // the chat's task list
}

type remoteApproval struct {
	Chat    string `json:"chat"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
}

func (ui *desktopUI) chatByID(id string) *conversation {
	for _, chat := range ui.chats {
		if chat.ID == id {
			return chat
		}
	}
	return nil
}

func chatSummary(chat *conversation) remoteChat {
	return remoteChat{ID: chat.ID, Title: chat.Title, Workspace: chat.Config.Workspace, Running: chat.run != nil, Items: len(chat.Items)}
}

func itemForPhone(chat *conversation, index int) remoteItem {
	item := chat.Items[index]
	out := remoteItem{Kind: item.Kind, Text: item.Text, Images: len(item.Images)}
	if item.Kind != "tool" {
		return out
	}
	state := runState(chat, index)
	verb, target, mono := describeTool(item, state != "done")
	out.Tool, out.Verb, out.Target, out.Mono, out.State = item.Tool, verb, target, mono, state
	out.Args, out.Diff, out.Millis, out.Failed = item.Args, item.Diff, item.Millis, item.Failed
	switch {
	case state == "pending":
		out.Meta, out.Tone = "Awaiting approval", "amber"
	case state == "running":
	case item.Failed == stoppedCall:
		out.Meta, out.Tone = "Stopped", "muted"
	case item.Failed != "":
		out.Meta, out.Tone = shortError(item.Failed), "danger"
	case item.Tool == "edit_file" && item.Diff != "":
		added, removed := diffStat(item.Diff)
		out.Meta, out.Tone = fmt.Sprintf("+%d −%d", added, removed), "added"
	case item.Tool == "shell" && item.Millis > 0:
		out.Meta, out.Tone = elapsed(item.Millis), "muted"
	}
	return out
}

func (ui *desktopUI) metaForPhone(chat *conversation) remoteMeta {
	remoteMetaValue := remoteMeta{Chat: chat.ID, Title: chat.Title, Running: chat.run != nil, Items: len(chat.Items), Queue: []string{}, Steer: []string{}}
	for _, message := range chat.queue {
		remoteMetaValue.Queue = append(remoteMetaValue.Queue, message.Content)
	}
	for _, messageValue := range chat.steer {
		remoteMetaValue.Steer = append(remoteMetaValue.Steer, messageValue.Content)
	}
	remoteMetaValue.Model, remoteMetaValue.Effort = ui.choiceLabel(chat.Config), chat.Config.Effort
	remoteMetaValue.Window = modelContextWindow(chat.Config)
	remoteMetaValue.Todo = todoForPhone(chat)
	for _, h := range chat.History {
		remoteMetaValue.Used = max(remoteMetaValue.Used, h.Usage)
	}
	if run := chat.run; run != nil {
		if run.turn.usage > 0 {
			remoteMetaValue.Used = run.turn.usage
		}
		remoteMetaValue.Label, remoteMetaValue.Mark = run.turn.label, run.turn.mark
		if run.approvalID != "" {
			remoteMetaValue.Approval = &remoteApproval{Chat: chat.ID, ID: run.approvalID, Title: run.approvalTitle, Preview: run.approvalPreview}
		}
	}
	return remoteMetaValue
}

func (server *remoteServer) state(writer http.ResponseWriter, _ *http.Request) {
	var out map[string]any
	server.onUI(func() {
		chats := []remoteChat{}
		for _, chat := range server.ui.chats {
			chats = append(chats, chatSummary(chat))
		}
		current := ""
		if server.ui.current != nil {
			current = server.ui.current.ID
		}
		projects := append([]string{}, server.ui.projects...)
		out = map[string]any{"name": server.name, "projects": projects, "workspace": server.ui.cfg.Workspace,
			"chats": chats, "current": current, "settings": server.ui.settingsForPhone()}
	})
	writeJSON(writer, http.StatusOK, out)
}

func (server *remoteServer) chat(writer http.ResponseWriter, req *http.Request) {
	var out map[string]any
	server.onUI(func() {
		chat := server.ui.chatByID(req.PathValue("id"))
		if chat == nil {
			return
		}
		items := make([]remoteItem, len(chat.Items))
		for i := range chat.Items {
			items[i] = itemForPhone(chat, i)
		}
		out = map[string]any{"chat": chatSummary(chat), "items": items, "meta": server.ui.metaForPhone(chat)}
	})
	if out == nil {
		fail(writer, http.StatusNotFound, "This chat no longer exists")
		return
	}
	writeJSON(writer, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Live events (server-sent events)

func (server *remoteServer) events(writer http.ResponseWriter, req *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		fail(writer, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)
	fmt.Fprint(writer, ": 1loop\n\n")
	flusher.Flush()
	ch := make(chan remoteEvent, 256)
	server.mu.Lock()
	server.subs[ch] = true
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		delete(server.subs, ch)
		server.mu.Unlock()
	}()
	ping := time.NewTicker(20 * time.Second) // keeps phones and routers from dropping an idle stream
	defer ping.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(writer, ": ping\n\n")
		case remoteEventValue, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", remoteEventValue.kind, remoteEventValue.data)
		}
		flusher.Flush()
	}
}

// broadcast hands an event to every phone; a phone too slow to keep up is
// dropped and reconnects with a fresh state.
func (server *remoteServer) broadcast(kind string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	for ch := range server.subs {
		select {
		case ch <- remoteEvent{kind, data}:
		default:
			close(ch)
			delete(server.subs, ch)
		}
	}
}

// The window reports its changes here (UI thread); they reach the phones in
// batches, at most ten times a second, so a streamed answer stays light.

func (server *remoteServer) itemChanged(chat *conversation, index int) {
	if server == nil || chat.ID == "" {
		return
	}
	if server.dirty[chat] == nil {
		server.dirty[chat] = map[int]bool{}
	}
	server.dirty[chat][index] = true
	server.meta[chat] = true
	server.schedule()
}

func (server *remoteServer) metaChanged(chat *conversation) {
	if server == nil || chat == nil || chat.ID == "" {
		return
	}
	server.meta[chat] = true
	server.schedule()
}

func (server *remoteServer) replaceChat(chat *conversation) {
	if server == nil {
		return
	}
	delete(server.dirty, chat)
	server.broadcast("replace", map[string]string{"chat": chat.ID})
	server.chatsChanged()
	server.metaChanged(chat)
	server.settingsChanged()
}

func (server *remoteServer) settingsChanged() {
	if server == nil {
		return
	}
	server.settings = true
	server.schedule()
}

func (server *remoteServer) chatsChanged() {
	if server == nil {
		return
	}
	server.chatsDirt = true
	server.schedule()
}

func (server *remoteServer) schedule() {
	if server.flushing {
		return
	}
	server.flushing = true
	time.AfterFunc(100*time.Millisecond, func() { fyne.Do(server.flush) })
}

func (server *remoteServer) flush() {
	server.flushing = false
	server.mu.Lock()
	listening := len(server.subs) > 0
	server.mu.Unlock()
	if !listening {
		server.dirty, server.meta, server.chatsDirt, server.settings = map[*conversation]map[int]bool{}, map[*conversation]bool{}, false, false
		return
	}
	if server.chatsDirt {
		chats := []remoteChat{}
		for _, chat := range server.ui.chats {
			chats = append(chats, chatSummary(chat))
		}
		server.broadcast("chats", map[string]any{"chats": chats})
	}
	for chat, indexes := range server.dirty {
		for index := range indexes {
			if index < len(chat.Items) {
				server.broadcast("item", map[string]any{"chat": chat.ID, "index": index, "item": itemForPhone(chat, index)})
			}
		}
	}
	for chat := range server.meta {
		server.broadcast("meta", server.ui.metaForPhone(chat))
	}
	if server.settings {
		server.broadcast("settings", server.ui.settingsForPhone())
	}
	server.dirty, server.meta, server.chatsDirt, server.settings = map[*conversation]map[int]bool{}, map[*conversation]bool{}, false, false
}

// ---------------------------------------------------------------------------
// Actions

func (server *remoteServer) send(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Chat      string `json:"chat"`
		Text      string `json:"text"`
		Mode      string `json:"mode"`
		Workspace string `json:"workspace"`
	}
	if readJSON(req, &body) != nil || strings.TrimSpace(body.Text) == "" {
		fail(writer, http.StatusBadRequest, "Nothing to send")
		return
	}
	var problem, chatID string
	server.onUI(func() {
		chatID, problem = server.ui.remoteSend(body.Chat, strings.TrimSpace(body.Text), body.Mode, body.Workspace)
	})
	if problem != "" {
		fail(writer, http.StatusConflict, problem)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"chat": chatID})
}

// remoteSend sends a phone's message as if it had been typed in the window:
// the chat opens on the computer and the message is sent, queued or steered.
func (ui *desktopUI) remoteSend(chatID, text, mode, workspace string) (string, string) {
	messageValue := message{Role: "user", Content: text}
	var chat *conversation
	if chatID != "" {
		if chat = ui.chatByID(chatID); chat == nil {
			return "", "This chat no longer exists"
		}
		if chat.Config.Workspace != ui.cfg.Workspace {
			ui.cfg.Workspace = chat.Config.Workspace
		}
		ui.openChat(chat)
	} else {
		if workspace != "" && workspace != ui.cfg.Workspace {
			if root, err := resolveWorkspace(workspace); err == nil {
				ui.cfg.Workspace = root
				ui.ensureProject(root)
			}
		}
		ui.startNew()
	}
	if chat != nil && chat.run != nil {
		if mode == "steer" {
			chat.steer = append(chat.steer, messageValue)
		} else {
			chat.queue = append(chat.queue, messageValue)
		}
		ui.renderQueue()
		ui.remote.metaChanged(chat)
		return chat.ID, ""
	}
	if problem := ui.notReady(); problem != "" {
		return "", problem
	}
	ui.start(messageValue)
	return ui.current.ID, ""
}

func (server *remoteServer) stop(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Chat string `json:"chat"`
	}
	readJSON(req, &body)
	server.onUI(func() {
		if chat := server.ui.chatByID(body.Chat); chat != nil && chat.run != nil {
			chat.run.cancel()
		}
	})
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (server *remoteServer) approve(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
	}
	if readJSON(req, &body) != nil {
		fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	found := false
	server.onUI(func() {
		for _, chat := range server.ui.chats {
			if run := chat.run; run != nil && run.approvalID == body.ID && run.answer != nil {
				run.answer(body.Allow) // closes the dialog on the computer too
				found = true
			}
		}
	})
	if !found {
		fail(writer, http.StatusNotFound, "This request was already answered")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// Models, reasoning effort and approval mode, as the composer offers them.

type remoteEffort struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type remoteModel struct {
	Label   string         `json:"label"` // unique key, as in the desktop picker
	Name    string         `json:"name"`
	Detail  string         `json:"detail"`
	Efforts []remoteEffort `json:"efforts"`
}

// choiceLabel finds the picker label of a configuration's model.
func (ui *desktopUI) choiceLabel(cfg config) string {
	for label, choice := range ui.choices {
		if choice.Connection.ID == cfg.ConnectionID && choice.ID == cfg.Model {
			return label
		}
	}
	return ""
}

func (ui *desktopUI) settingsForPhone() map[string]any {
	models := []remoteModel{}
	for _, label := range ui.model.Options {
		choice := ui.choices[label]
		remoteModelValue := remoteModel{Label: label, Name: choice.display(), Detail: ui.model.Details[label], Efforts: []remoteEffort{}}
		for _, e := range choice.Efforts {
			remoteModelValue.Efforts = append(remoteModelValue.Efforts, remoteEffort{e, effortLabel(e)})
		}
		models = append(models, remoteModelValue)
	}
	return map[string]any{"models": models, "model": ui.choiceLabel(ui.cfg), "effort": ui.cfg.Effort,
		"approvalMode": approvalFor(ui.approvalMode, ui.cfg), "autoMode": ui.cfg.Provider == "claudecode", "loading": ui.modelsLoading, "window": modelContextWindow(ui.cfg)}
}

func (server *remoteServer) setModel(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Chat   string `json:"chat"`
		Label  string `json:"label"`
		Effort string `json:"effort"`
	}
	if readJSON(req, &body) != nil {
		fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	problem := ""
	server.onUI(func() { problem = server.ui.remoteModel(body.Chat, body.Label, body.Effort) })
	if problem != "" {
		fail(writer, http.StatusConflict, problem)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// remoteModel picks a chat's model and effort from the phone, through the
// same picker as the window (the chat opens there to show it).
func (ui *desktopUI) remoteModel(chatID, label, effort string) string {
	if chatID != "" {
		chat := ui.chatByID(chatID)
		if chat == nil {
			return "This chat no longer exists"
		}
		if chat != ui.current {
			ui.cfg.Workspace = chat.Config.Workspace
			ui.openChat(chat)
		}
	} else if ui.current != nil {
		ui.startNew()
	}
	if label != "" && label != ui.choiceLabel(ui.cfg) {
		choice, ok := ui.choices[label]
		if !ok {
			return "This model is no longer available"
		}
		ui.model.Selected = label
		ui.selectModel(choice)
	}
	if effort != "" {
		choice, ok := ui.selectedChoice()
		if !ok || !contains(choice.Efforts, effort) {
			return "This model has no such reasoning level"
		}
		ui.cfg.Effort = effort
		if ui.current != nil {
			ui.current.Config.Effort = effort
		}
		ui.model.row.SetTrailing(effortLabel(effort))
		if ui.model.relayout != nil {
			ui.model.relayout()
		}
		ui.save()
		ui.remote.settingsChanged()
		ui.remote.metaChanged(ui.current)
	}
	return ""
}

func (server *remoteServer) setApprovalMode(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if readJSON(req, &body) != nil || (body.Mode != "approval" && body.Mode != "yolo" && body.Mode != "auto") {
		fail(writer, http.StatusBadRequest, "Invalid mode")
		return
	}
	server.onUI(func() {
		server.ui.approvalMode = body.Mode
		server.ui.updateApprovalControl()
		server.ui.save()
	})
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// Settings: the remote switch and the paired phones.

func (ui *desktopUI) remoteSettings() fyne.CanvasObject {
	describe := func() string {
		switch {
		case ui.remote != nil:
			return fmt.Sprintf("Listening on port %d as “%s”. A phone must be paired with a code shown on this screen.", ui.remote.port, ui.remote.name)
		case ui.remoteEnabled:
			return "The remote could not start: the network is unavailable."
		}
		return "Off: phones can neither find nor reach this computer."
	}
	enabled := newToggle(ui.remoteEnabled, func(on bool) {
		ui.remoteEnabled = on
		if on && ui.remote == nil {
			ui.startRemote()
		} else if !on && ui.remote != nil {
			ui.remote.close()
			ui.remote = nil
		}
		ui.save()
		ui.prefs.show("Phone remote") // the state line and the list follow
	})
	var phones []fyne.CanvasObject
	for i, pairedDevice := range ui.devices {
		i := i
		remove := newRow("Forget", iconTrash, styleChip, func() {
			ui.devices = append(ui.devices[:i], ui.devices[i+1:]...)
			ui.save()
			ui.prefs.show("Phone remote")
		})
		phones = append(phones, settingRow(pairedDevice.Name, "Paired "+pairedDevice.Added.Format("2 Jan 2006 15:04"), remove))
	}
	if len(phones) == 0 {
		phones = append(phones, settingRow("No paired phone yet", "Open the 1loop app on a phone connected to the same network.", nil))
	}
	return container.NewVBox(
		ui.prefs.pageTitle("Phone remote", "", nil),
		card(settingRow("Allow the 1loop phone app on this network", describe(), enabled)),
		sectionTitle("Paired phones"),
		card(phones...),
	)
}

// remoteDefaults gives conversations saved before the remote an id.
func (ui *desktopUI) remoteDefaults() {
	for _, chat := range ui.chats {
		if chat.ID == "" {
			chat.ID = identifier()[:16]
		}
	}
}
