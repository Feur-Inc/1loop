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
	u     *desktopUI
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
func (u *desktopUI) startRemote() {
	if !u.remoteEnabled {
		return
	}
	host, _ := os.Hostname()
	r := &remoteServer{u: u, name: host, subs: map[chan remoteEvent]bool{}, pairs: map[string]*pairing{},
		dirty: map[*conversation]map[int]bool{}, meta: map[*conversation]bool{}}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", remoteHTTPPort))
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		return // no network: the window works without its remote
	}
	r.port = ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", r.info)
	mux.HandleFunc("POST /api/pair", r.pair)
	mux.HandleFunc("POST /api/pair/confirm", r.confirmPair)
	mux.HandleFunc("GET /api/state", r.authorized(r.state))
	mux.HandleFunc("GET /api/chats/{id}", r.authorized(r.chat))
	mux.HandleFunc("GET /api/events", r.authorized(r.events))
	mux.HandleFunc("POST /api/send", r.authorized(r.send))
	mux.HandleFunc("POST /api/stop", r.authorized(r.stop))
	mux.HandleFunc("POST /api/approve", r.authorized(r.approve))
	mux.HandleFunc("POST /api/model", r.authorized(r.setModel))
	mux.HandleFunc("POST /api/approval-mode", r.authorized(r.setApprovalMode))
	r.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go r.http.Serve(ln)
	if udp, err := net.ListenUDP("udp4", &net.UDPAddr{Port: remoteDiscoveryPort}); err == nil {
		r.udp = udp
		go r.answerDiscovery()
	}
	u.remote = r
}

func (r *remoteServer) close() {
	if r == nil {
		return
	}
	r.http.Close()
	if r.udp != nil {
		r.udp.Close()
	}
}

// answerDiscovery replies to every "1loop?" broadcast with where to connect.
func (r *remoteServer) answerDiscovery() {
	buf := make([]byte, 512)
	for {
		n, from, err := r.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if strings.TrimSpace(string(buf[:n])) != "1loop?" {
			continue
		}
		reply, _ := json.Marshal(map[string]any{"service": "1loop", "name": r.name, "port": r.port, "protocol": remoteProtocol})
		r.udp.WriteToUDP(reply, from)
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(req *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(v)
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// authorized lets a request through only with a paired device's token.
func (r *remoteServer) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = req.URL.Query().Get("token") // event streams cannot always set headers
		}
		hash := tokenHash(token)
		ok := false
		r.onUI(func() {
			for _, d := range r.u.devices {
				if subtle.ConstantTimeCompare([]byte(d.TokenHash), []byte(hash)) == 1 {
					ok = true
				}
			}
		})
		if !ok {
			fail(w, http.StatusUnauthorized, "This phone is not paired with 1loop")
			return
		}
		next(w, req)
	}
}

// onUI runs f on the window's thread, where all chat state lives.
func (r *remoteServer) onUI(f func()) {
	if r.u.ctx.Err() == nil {
		fyne.DoAndWait(f)
	}
}

// ---------------------------------------------------------------------------
// Pairing

func (r *remoteServer) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"service": "1loop", "name": r.name, "protocol": remoteProtocol})
}

func (r *remoteServer) pair(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Device string `json:"device"`
	}
	if readJSON(req, &body) != nil {
		fail(w, http.StatusBadRequest, "Invalid request")
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
	p := &pairing{device: device, code: fmt.Sprintf("%06d", n.Int64()), expires: time.Now().Add(2 * time.Minute)}
	id := identifier()
	r.onUI(func() {
		for key, old := range r.pairs { // one pending pairing at a time
			if old.sheet != nil {
				old.sheet.Hide()
			}
			delete(r.pairs, key)
		}
		r.pairs[id] = p
		p.sheet = r.u.showPairing(p)
	})
	writeJSON(w, http.StatusOK, map[string]string{"pairing": id})
}

func (r *remoteServer) confirmPair(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Pairing string `json:"pairing"`
		Code    string `json:"code"`
	}
	if readJSON(req, &body) != nil {
		fail(w, http.StatusBadRequest, "Invalid request")
		return
	}
	var token, problem string
	r.onUI(func() {
		p := r.pairs[body.Pairing]
		switch {
		case p == nil || time.Now().After(p.expires):
			problem = "This code has expired. Ask for a new one."
		case subtle.ConstantTimeCompare([]byte(p.code), []byte(strings.TrimSpace(body.Code))) != 1:
			p.tries++
			problem = "Wrong code."
			if p.tries >= 5 { // a guessed code would give the shell away
				problem = "Too many wrong codes. Ask for a new one."
				p.expires = time.Time{}
			}
		default:
			token = identifier()
			r.u.devices = append(r.u.devices, pairedDevice{Name: p.device, TokenHash: tokenHash(token), Added: time.Now()})
			r.u.save()
			if p.sheet != nil {
				p.sheet.Hide()
			}
			delete(r.pairs, body.Pairing)
		}
		if problem != "" && p != nil && p.expires.IsZero() && p.sheet != nil {
			p.sheet.Hide()
		}
	})
	if problem != "" {
		fail(w, http.StatusForbidden, problem)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token, "name": r.name})
}

// showPairing shows the code a phone must type.
func (u *desktopUI) showPairing(p *pairing) *sheet {
	code := textLine(p.code[:3]+" "+p.code[3:], 40, colAmber)
	code.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	intro := widget.NewLabel("“" + p.device + "” wants to control 1loop from the phone. Type this code in the app to allow it. It expires in two minutes.")
	intro.Wrapping = fyne.TextWrapWord
	hint := note("A paired phone can send messages and approve commands. Remove it any time in Settings.")
	body := container.NewVBox(intro, spacer(0, 6), container.NewCenter(code), spacer(0, 6), hint)
	var s *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { s.dismiss() })
	s = u.modal("Pair a phone", body, 460, func() { p.expires = time.Time{} }, cancel)
	return s
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
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Used   int    `json:"used"`
	Window int    `json:"window"`
}

type remoteApproval struct {
	Chat    string `json:"chat"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Preview string `json:"preview"`
}

func (u *desktopUI) chatByID(id string) *conversation {
	for _, chat := range u.chats {
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

func (u *desktopUI) metaForPhone(chat *conversation) remoteMeta {
	m := remoteMeta{Chat: chat.ID, Title: chat.Title, Running: chat.run != nil, Items: len(chat.Items), Queue: []string{}, Steer: []string{}}
	for _, q := range chat.queue {
		m.Queue = append(m.Queue, q.Content)
	}
	for _, s := range chat.steer {
		m.Steer = append(m.Steer, s.Content)
	}
	m.Model, m.Effort = u.choiceLabel(chat.Config), chat.Config.Effort
	m.Window = modelContextWindow(chat.Config)
	for _, h := range chat.History {
		m.Used = max(m.Used, h.Usage)
	}
	if run := chat.run; run != nil {
		if run.turn.usage > 0 {
			m.Used = run.turn.usage
		}
		m.Label, m.Mark = run.turn.label, run.turn.mark
		if run.approvalID != "" {
			m.Approval = &remoteApproval{Chat: chat.ID, ID: run.approvalID, Title: run.approvalTitle, Preview: run.approvalPreview}
		}
	}
	return m
}

func (r *remoteServer) state(w http.ResponseWriter, _ *http.Request) {
	var out map[string]any
	r.onUI(func() {
		chats := []remoteChat{}
		for _, chat := range r.u.chats {
			chats = append(chats, chatSummary(chat))
		}
		current := ""
		if r.u.current != nil {
			current = r.u.current.ID
		}
		projects := append([]string{}, r.u.projects...)
		out = map[string]any{"name": r.name, "projects": projects, "workspace": r.u.cfg.Workspace,
			"chats": chats, "current": current, "settings": r.u.settingsForPhone()}
	})
	writeJSON(w, http.StatusOK, out)
}

func (r *remoteServer) chat(w http.ResponseWriter, req *http.Request) {
	var out map[string]any
	r.onUI(func() {
		chat := r.u.chatByID(req.PathValue("id"))
		if chat == nil {
			return
		}
		items := make([]remoteItem, len(chat.Items))
		for i := range chat.Items {
			items[i] = itemForPhone(chat, i)
		}
		out = map[string]any{"chat": chatSummary(chat), "items": items, "meta": r.u.metaForPhone(chat)}
	})
	if out == nil {
		fail(w, http.StatusNotFound, "This chat no longer exists")
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Live events (server-sent events)

func (r *remoteServer) events(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": 1loop\n\n")
	flusher.Flush()
	ch := make(chan remoteEvent, 256)
	r.mu.Lock()
	r.subs[ch] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.subs, ch)
		r.mu.Unlock()
	}()
	ping := time.NewTicker(20 * time.Second) // keeps phones and routers from dropping an idle stream
	defer ping.Stop()
	for {
		select {
		case <-req.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case e, open := <-ch:
			if !open {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.kind, e.data)
		}
		flusher.Flush()
	}
}

// broadcast hands an event to every phone; a phone too slow to keep up is
// dropped and reconnects with a fresh state.
func (r *remoteServer) broadcast(kind string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for ch := range r.subs {
		select {
		case ch <- remoteEvent{kind, data}:
		default:
			close(ch)
			delete(r.subs, ch)
		}
	}
}

// The window reports its changes here (UI thread); they reach the phones in
// batches, at most ten times a second, so a streamed answer stays light.

func (r *remoteServer) itemChanged(chat *conversation, index int) {
	if r == nil || chat.ID == "" {
		return
	}
	if r.dirty[chat] == nil {
		r.dirty[chat] = map[int]bool{}
	}
	r.dirty[chat][index] = true
	r.meta[chat] = true
	r.schedule()
}

func (r *remoteServer) metaChanged(chat *conversation) {
	if r == nil || chat == nil || chat.ID == "" {
		return
	}
	r.meta[chat] = true
	r.schedule()
}

func (r *remoteServer) replaceChat(chat *conversation) {
	if r == nil {
		return
	}
	delete(r.dirty, chat)
	r.broadcast("replace", map[string]string{"chat": chat.ID})
	r.chatsChanged()
	r.metaChanged(chat)
	r.settingsChanged()
}

func (r *remoteServer) settingsChanged() {
	if r == nil {
		return
	}
	r.settings = true
	r.schedule()
}

func (r *remoteServer) chatsChanged() {
	if r == nil {
		return
	}
	r.chatsDirt = true
	r.schedule()
}

func (r *remoteServer) schedule() {
	if r.flushing {
		return
	}
	r.flushing = true
	time.AfterFunc(100*time.Millisecond, func() { fyne.Do(r.flush) })
}

func (r *remoteServer) flush() {
	r.flushing = false
	r.mu.Lock()
	listening := len(r.subs) > 0
	r.mu.Unlock()
	if !listening {
		r.dirty, r.meta, r.chatsDirt, r.settings = map[*conversation]map[int]bool{}, map[*conversation]bool{}, false, false
		return
	}
	if r.chatsDirt {
		chats := []remoteChat{}
		for _, chat := range r.u.chats {
			chats = append(chats, chatSummary(chat))
		}
		r.broadcast("chats", map[string]any{"chats": chats})
	}
	for chat, indexes := range r.dirty {
		for index := range indexes {
			if index < len(chat.Items) {
				r.broadcast("item", map[string]any{"chat": chat.ID, "index": index, "item": itemForPhone(chat, index)})
			}
		}
	}
	for chat := range r.meta {
		r.broadcast("meta", r.u.metaForPhone(chat))
	}
	if r.settings {
		r.broadcast("settings", r.u.settingsForPhone())
	}
	r.dirty, r.meta, r.chatsDirt, r.settings = map[*conversation]map[int]bool{}, map[*conversation]bool{}, false, false
}

// ---------------------------------------------------------------------------
// Actions

func (r *remoteServer) send(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Chat      string `json:"chat"`
		Text      string `json:"text"`
		Mode      string `json:"mode"`
		Workspace string `json:"workspace"`
	}
	if readJSON(req, &body) != nil || strings.TrimSpace(body.Text) == "" {
		fail(w, http.StatusBadRequest, "Nothing to send")
		return
	}
	var problem, chatID string
	r.onUI(func() {
		chatID, problem = r.u.remoteSend(body.Chat, strings.TrimSpace(body.Text), body.Mode, body.Workspace)
	})
	if problem != "" {
		fail(w, http.StatusConflict, problem)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"chat": chatID})
}

// remoteSend sends a phone's message as if it had been typed in the window:
// the chat opens on the computer and the message is sent, queued or steered.
func (u *desktopUI) remoteSend(chatID, text, mode, workspace string) (string, string) {
	m := message{Role: "user", Content: text}
	var chat *conversation
	if chatID != "" {
		if chat = u.chatByID(chatID); chat == nil {
			return "", "This chat no longer exists"
		}
		if chat.Config.Workspace != u.cfg.Workspace {
			u.cfg.Workspace = chat.Config.Workspace
		}
		u.openChat(chat)
	} else {
		if workspace != "" && workspace != u.cfg.Workspace {
			if root, err := resolveWorkspace(workspace); err == nil {
				u.cfg.Workspace = root
				u.ensureProject(root)
			}
		}
		u.startNew()
	}
	if chat != nil && chat.run != nil {
		if mode == "steer" {
			chat.steer = append(chat.steer, m)
		} else {
			chat.queue = append(chat.queue, m)
		}
		u.renderQueue()
		u.remote.metaChanged(chat)
		return chat.ID, ""
	}
	if problem := u.notReady(); problem != "" {
		return "", problem
	}
	u.start(m)
	return u.current.ID, ""
}

func (r *remoteServer) stop(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Chat string `json:"chat"`
	}
	readJSON(req, &body)
	r.onUI(func() {
		if chat := r.u.chatByID(body.Chat); chat != nil && chat.run != nil {
			chat.run.cancel()
		}
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (r *remoteServer) approve(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
	}
	if readJSON(req, &body) != nil {
		fail(w, http.StatusBadRequest, "Invalid request")
		return
	}
	found := false
	r.onUI(func() {
		for _, chat := range r.u.chats {
			if run := chat.run; run != nil && run.approvalID == body.ID && run.answer != nil {
				run.answer(body.Allow) // closes the dialog on the computer too
				found = true
			}
		}
	})
	if !found {
		fail(w, http.StatusNotFound, "This request was already answered")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
func (u *desktopUI) choiceLabel(cfg config) string {
	for label, choice := range u.choices {
		if choice.Connection.ID == cfg.ConnectionID && choice.ID == cfg.Model {
			return label
		}
	}
	return ""
}

func (u *desktopUI) settingsForPhone() map[string]any {
	models := []remoteModel{}
	for _, label := range u.model.Options {
		choice := u.choices[label]
		m := remoteModel{Label: label, Name: choice.display(), Detail: u.model.Details[label], Efforts: []remoteEffort{}}
		for _, e := range choice.Efforts {
			m.Efforts = append(m.Efforts, remoteEffort{e, effortLabel(e)})
		}
		models = append(models, m)
	}
	return map[string]any{"models": models, "model": u.choiceLabel(u.cfg), "effort": u.cfg.Effort,
		"approvalMode": approvalFor(u.approvalMode, u.cfg), "autoMode": u.cfg.Provider == "claudecode", "loading": u.modelsLoading, "window": modelContextWindow(u.cfg)}
}

func (r *remoteServer) setModel(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Chat   string `json:"chat"`
		Label  string `json:"label"`
		Effort string `json:"effort"`
	}
	if readJSON(req, &body) != nil {
		fail(w, http.StatusBadRequest, "Invalid request")
		return
	}
	problem := ""
	r.onUI(func() { problem = r.u.remoteModel(body.Chat, body.Label, body.Effort) })
	if problem != "" {
		fail(w, http.StatusConflict, problem)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// remoteModel picks a chat's model and effort from the phone, through the
// same picker as the window (the chat opens there to show it).
func (u *desktopUI) remoteModel(chatID, label, effort string) string {
	if chatID != "" {
		chat := u.chatByID(chatID)
		if chat == nil {
			return "This chat no longer exists"
		}
		if chat != u.current {
			u.cfg.Workspace = chat.Config.Workspace
			u.openChat(chat)
		}
	} else if u.current != nil {
		u.startNew()
	}
	if label != "" && label != u.choiceLabel(u.cfg) {
		choice, ok := u.choices[label]
		if !ok {
			return "This model is no longer available"
		}
		u.model.Selected = label
		u.selectModel(choice)
	}
	if effort != "" {
		choice, ok := u.selectedChoice()
		if !ok || !contains(choice.Efforts, effort) {
			return "This model has no such reasoning level"
		}
		u.cfg.Effort = effort
		if u.current != nil {
			u.current.Config.Effort = effort
		}
		u.model.row.SetTrailing(effortLabel(effort))
		if u.model.relayout != nil {
			u.model.relayout()
		}
		u.save()
		u.remote.settingsChanged()
		u.remote.metaChanged(u.current)
	}
	return ""
}

func (r *remoteServer) setApprovalMode(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if readJSON(req, &body) != nil || (body.Mode != "approval" && body.Mode != "yolo" && body.Mode != "auto") {
		fail(w, http.StatusBadRequest, "Invalid mode")
		return
	}
	r.onUI(func() {
		r.u.approvalMode = body.Mode
		r.u.updateApprovalControl()
		r.u.save()
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// Settings: the remote switch and the paired phones.

func (u *desktopUI) remoteSettings() fyne.CanvasObject {
	describe := func() string {
		switch {
		case u.remote != nil:
			return fmt.Sprintf("Listening on port %d as “%s”. A phone must be paired with a code shown on this screen.", u.remote.port, u.remote.name)
		case u.remoteEnabled:
			return "The remote could not start: the network is unavailable."
		}
		return "Off: phones can neither find nor reach this computer."
	}
	enabled := newToggle(u.remoteEnabled, func(on bool) {
		u.remoteEnabled = on
		if on && u.remote == nil {
			u.startRemote()
		} else if !on && u.remote != nil {
			u.remote.close()
			u.remote = nil
		}
		u.save()
		u.prefs.show("Phone remote") // the state line and the list follow
	})
	var phones []fyne.CanvasObject
	for i, d := range u.devices {
		i := i
		remove := newRow("Forget", iconTrash, styleChip, func() {
			u.devices = append(u.devices[:i], u.devices[i+1:]...)
			u.save()
			u.prefs.show("Phone remote")
		})
		phones = append(phones, settingRow(d.Name, "Paired "+d.Added.Format("2 Jan 2006 15:04"), remove))
	}
	if len(phones) == 0 {
		phones = append(phones, settingRow("No paired phone yet", "Open the 1loop app on a phone connected to the same network.", nil))
	}
	return container.NewVBox(
		u.prefs.pageTitle("Phone remote", "", nil),
		card(settingRow("Allow the 1loop phone app on this network", describe(), enabled)),
		sectionTitle("Paired phones"),
		card(phones...),
	)
}

// remoteDefaults gives conversations saved before the remote an id.
func (u *desktopUI) remoteDefaults() {
	for _, chat := range u.chats {
		if chat.ID == "" {
			chat.ID = identifier()[:16]
		}
	}
}
