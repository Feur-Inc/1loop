package ui

// Phone remote: 1loop serves its chats on the local network so the 1loop
// Android app can follow them and drive them. Everything a phone does goes
// through the same functions as the window, so it shows on the computer.
//
// Discovery: the phone broadcasts "1loop?" on UDP remoteDiscoveryPort and
// every 1loop on the network answers with its name, HTTPS port and
// certificate fingerprint (a hint only: anyone can answer a broadcast).
//
// Transport: HTTPS with this computer's own certificate (remote/server.crt in
// the config folder), which phones pin by its SHA-256 fingerprint.
//
// Pairing (remoteapi.PairingBook): the user opens a QR code on the computer
// (Settings → Phone remote), which gives the phone the address, the
// fingerprint and a secret; or a phone asks and the user types the six
// digits the computer shows, after checking the fingerprint's start shown
// on both. The phone then gets a token; only its hash is kept. Every other
// request carries the token in its Authorization header: the agent runs
// commands on this computer, so nobody on the network may drive it without
// the user's consent.
//
// API (JSON over HTTPS):
//   GET  /api/info                      name and version, no token needed
//   POST /api/pair       {device}       → {pairing}   (the code shows on screen)
//   POST /api/pair/confirm {pairing,code,device} → {token}
//   GET  /api/state                     projects, chats, settings
//   GET  /api/chats/{id}                one chat with its items
//   GET  /api/events                    server-sent events (see remoteEvent)
//   POST /api/send       {chat,text,mode,workspace}  mode: send, queue or steer
//   POST /api/stop       {chat}
//   POST /api/approve    {id,allow}
//   POST /api/answer     {id,answers}         answers to the model's questions
//   POST /api/model      {chat,label,effort}    model and reasoning effort
//   GET  /api/limits                    every account's usage windows
//   POST /api/approval-mode {mode}              "approval" or "yolo"
//   /api/chats/{id}/panel…                      the chat's side panel (phone_panel.go)

import (
	"bytes"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/skip2/go-qrcode"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/extensions"
	"1loop/internal/githubconnector"
	"1loop/internal/platform"
	"1loop/internal/remoteapi"
)

const (
	remoteDiscoveryPort = 47100
	remoteHTTPPort      = 47101
	remoteProtocol      = 2 // 2: HTTPS with a pinned certificate
)

type remoteEvent struct {
	kind string
	data []byte
}

type remoteServer struct {
	ui   *desktopUI
	name string
	port int
	http *http.Server
	udp  *net.UDPConn
	mu   sync.Mutex
	subs map[chan remoteEvent]bool
	// Browser streams, closed with the event streams when a phone is forgotten.
	panelHomes  map[string]*conversation // project panels before the first prompt; not persisted
	streams     map[chan struct{}]bool
	fingerprint string

	// The pending pairing and its sheet (UI thread only).
	book      remoteapi.PairingBook
	pairSheet *sheet

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
	config, err := os.UserConfigDir()
	if err != nil {
		return // no private folder for the certificate: the window works without its remote
	}
	certificate, err := remoteapi.Certificate(filepath.Join(config, "1loop", "remote"), "", "")
	if err != nil {
		platform.Devf("remote", "certificate: %v", err)
		return
	}
	server := &remoteServer{ui: ui, name: host, subs: map[chan remoteEvent]bool{}, streams: map[chan struct{}]bool{},
		fingerprint: remoteapi.Fingerprint(certificate), dirty: map[*conversation]map[int]bool{}, meta: map[*conversation]bool{}}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", remoteHTTPPort))
	if err != nil {
		ln, err = net.Listen("tcp", ":0")
	}
	if err != nil {
		return // no network: the window works without its remote
	}
	server.port = ln.Addr().(*net.TCPAddr).Port
	ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/info", server.info)
	mux.HandleFunc("POST /api/pair", server.pair)
	mux.HandleFunc("POST /api/pair/confirm", server.confirmPair)
	mux.HandleFunc("GET /api/state", server.authorized(server.state))
	mux.HandleFunc("GET /api/chats/{id}", server.authorized(server.chat))
	mux.HandleFunc("POST /api/chats/{id}/delete", server.authorized(server.deleteChat))
	mux.HandleFunc("POST /api/chats/{id}/tasks/{task}/stop", server.authorized(server.stopTask))
	mux.HandleFunc("POST /api/browser/clear", server.authorized(server.clearBrowser))
	for path, handler := range extensions.Handlers(func() {
		server.broadcast("extensions", map[string]bool{"changed": true})
		server.onUI(server.ui.extensionsChanged)
	}) {
		mux.HandleFunc(path, server.authorized(handler))
	}
	for path, handler := range githubconnector.Handlers() {
		mux.HandleFunc(path, server.authorized(handler))
	}
	mux.HandleFunc("GET /api/hooks", server.authorized(remoteapi.GetHooks))
	mux.HandleFunc("POST /api/hooks", server.authorized(remoteapi.SaveHooks))
	mux.HandleFunc("GET /api/chats/{id}/items/{index}/images/{n}", server.authorized(server.itemImage))
	mux.HandleFunc("GET /api/events", server.authorized(server.events))
	mux.HandleFunc("POST /api/chats/{id}/message", server.authorized(server.editMessage))
	mux.HandleFunc("POST /api/chats/{id}/version", server.authorized(server.selectVersion))
	mux.HandleFunc("POST /api/attachments", server.authorized(server.uploadAttachment))
	syncEngine := server.ui.syncEngine()
	syncEngine.Identity = server.fingerprint
	syncManifest, syncExport, syncImport := syncEngine.Routes()
	mux.HandleFunc("GET /api/sync/base", server.authorized(syncEngine.BaseRoute))
	mux.HandleFunc("POST /api/sync/base", server.authorized(syncEngine.BaseRoute))
	mux.HandleFunc("GET /api/sync/manifest", server.authorized(syncManifest))
	mux.HandleFunc("POST /api/sync/export", server.authorized(syncExport))
	mux.HandleFunc("POST /api/sync/import", server.authorized(syncImport))
	mux.HandleFunc("GET /api/files", server.authorized(server.projectFiles(remoteapi.ListFiles)))
	mux.HandleFunc("GET /api/files/content", server.authorized(server.projectFiles(remoteapi.FileContent)))
	mux.HandleFunc("GET /api/files/download", server.authorized(server.projectFiles(remoteapi.DownloadFile)))
	mux.HandleFunc("POST /api/files/upload", server.authorized(server.projectFiles(remoteapi.UploadFile)))
	mux.HandleFunc("POST /api/send", server.authorized(server.send))
	mux.HandleFunc("POST /api/stop", server.authorized(server.stop))
	mux.HandleFunc("POST /api/approve", server.authorized(server.approve))
	mux.HandleFunc("POST /api/answer", server.authorized(server.answer))
	mux.HandleFunc("POST /api/preferences", server.authorized(server.setPreferences))
	mux.HandleFunc("POST /api/memory", server.authorized(server.setMemory))
	mux.HandleFunc("GET /api/projects", server.authorized(server.projectFolders))
	mux.HandleFunc("POST /api/projects", server.authorized(server.addProject))
	mux.HandleFunc("POST /api/projects/{action}", server.authorized(server.changeProject))
	mux.HandleFunc("POST /api/model", server.authorized(server.setModel))
	mux.HandleFunc("POST /api/auto-resume", server.authorized(server.setAutoResume))
	mux.HandleFunc("POST /api/model/compaction", server.authorized(server.setModelCompaction))
	mux.HandleFunc("GET /api/limits", server.authorized(server.limits))
	mux.HandleFunc("POST /api/approval-mode", server.authorized(server.setApprovalMode))
	mux.HandleFunc("POST /api/chats/{id}/panel", server.authorized(server.editPanel))
	mux.HandleFunc("GET /api/chats/{id}/panel", server.authorized(server.panelTabs))
	mux.HandleFunc("GET /api/chats/{id}/panel/{key}", server.authorized(server.panelTab))
	mux.HandleFunc("GET /api/chats/{id}/panel/{key}/screen", server.authorized(server.panelScreen))
	mux.HandleFunc("GET /api/chats/{id}/panel/{key}/stream", server.authorized(server.panelStream))
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
	for _, home := range server.panelHomes {
		home.disposePanel()
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
		reply, _ := json.Marshal(map[string]any{"service": "1loop", "name": server.name, "port": server.port, "protocol": remoteProtocol, "scheme": "https", "fingerprint": server.fingerprint})
		server.udp.WriteToUDP(reply, from)
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers

// authorized lets a request through only with a paired device's token.
func (server *remoteServer) authorized(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" || token == req.Header.Get("Authorization") {
			core.Fail(writer, http.StatusUnauthorized, "This phone is not paired with 1loop")
			return
		}
		hash := remoteapi.TokenHash(token)
		ok := false
		server.onUI(func() {
			for _, pairedDevice := range server.ui.devices {
				if subtle.ConstantTimeCompare([]byte(pairedDevice.TokenHash), []byte(hash)) == 1 {
					ok = true
				}
			}
		})
		if !ok {
			core.Fail(writer, http.StatusUnauthorized, "This phone is not paired with 1loop")
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
	core.WriteJSON(writer, http.StatusOK, map[string]any{"service": "1loop", "name": server.name, "protocol": remoteProtocol})
}

func (server *remoteServer) pair(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Device string `json:"device"`
	}
	if core.ReadJSON(req, &body) != nil {
		core.Fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	var id string
	var err error
	server.onUI(func() {
		var pending *remoteapi.Pairing
		if pending, err = server.book.Request(body.Device); err == nil {
			id = pending.ID
			server.showPairSheet(server.ui.showPairing(pending, server.fingerprint))
		}
	})
	if err != nil {
		core.Fail(writer, http.StatusTooManyRequests, err.Error())
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]string{"pairing": id})
}

// showPairSheet replaces the sheet of an earlier pairing.
func (server *remoteServer) showPairSheet(next *sheet) {
	if server.pairSheet != nil {
		server.pairSheet.Hide()
	}
	server.pairSheet = next
}

func (server *remoteServer) confirmPair(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Pairing string `json:"pairing"`
		Code    string `json:"code"`
		Device  string `json:"device"`
	}
	if core.ReadJSON(req, &body) != nil {
		core.Fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	var token string
	var err error
	server.onUI(func() {
		var paired *remoteapi.Pairing
		paired, err = server.book.Confirm(body.Pairing, body.Code, body.Device)
		if server.book.Pending == nil {
			server.showPairSheet(nil)
		}
		if errors.Is(err, remoteapi.ErrPairingLocked) {
			server.ui.notice("Phone pairing paused", "Too many wrong pairing codes were typed. To pair a phone, open Settings → Phone remote and choose Pair a phone.")
		}
		if err != nil {
			return
		}
		token = core.Identifier()
		server.ui.devices = append(server.ui.devices, remoteapi.PairedDevice{Name: paired.Device, TokenHash: remoteapi.TokenHash(token), Added: time.Now()})
		server.ui.save()
		if server.ui.prefs.open && server.ui.prefs.section == "Phone remote" {
			server.ui.prefs.show("Phone remote")
		}
	})
	if err != nil {
		core.Fail(writer, http.StatusForbidden, err.Error())
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]string{"token": token, "name": server.name})
}

// disconnectAll closes every phone's streams: a forgotten phone's requests
// fail from now on, the others reconnect with their token.
func (server *remoteServer) disconnectAll() {
	server.mu.Lock()
	defer server.mu.Unlock()
	for ch := range server.subs {
		close(ch)
		delete(server.subs, ch)
	}
	for done := range server.streams {
		close(done)
		delete(server.streams, done)
	}
}

// fingerprintCheck is the start of the certificate fingerprint, shown on the
// computer and on the phone so the user can tell they talk to each other.
func fingerprintCheck(fingerprint string) string {
	short := strings.ToUpper(fingerprint[:min(16, len(fingerprint))])
	var groups []string
	for len(short) > 4 {
		groups, short = append(groups, short[:4]), short[4:]
	}
	return strings.Join(append(groups, short), " ")
}

// showPairing shows the code a phone must type.
func (ui *desktopUI) showPairing(pending *remoteapi.Pairing, fingerprint string) *sheet {
	code := textLine(pending.Code[:3]+" "+pending.Code[3:], 40, colAmber)
	code.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	intro := widget.NewLabel("“" + pending.Device + "” wants to control 1loop from the phone. Type this code in the app to allow it. It expires in two minutes.")
	intro.Wrapping = fyne.TextWrapWord
	check := note("The phone must show the same check: " + fingerprintCheck(fingerprint))
	hint := note("A paired phone can send messages and approve commands. Remove it any time in Settings.")
	body := container.NewVBox(intro, spacer(0, 6), container.NewCenter(code), spacer(0, 6), check, hint)
	server := ui.remote
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal("Pair a phone", body, 460, func() { server.book.Cancel(pending.ID) }, cancel)
	return dialog
}

// showPairingQR is the user pairing a phone from the computer: a QR code with
// this computer's addresses, its fingerprint and a one-time secret.
func (ui *desktopUI) showPairingQR() {
	server := ui.remote
	if server == nil {
		return
	}
	pending, err := server.book.Offer()
	if err != nil {
		ui.notice("Pair a phone", err.Error())
		return
	}
	link := url.URL{Scheme: "oneloop", Host: "pair", RawQuery: url.Values{
		"v": {"1"}, "n": {server.name}, "p": {fmt.Sprint(server.port)}, "f": {server.fingerprint},
		"i": {pending.ID}, "s": {pending.Code}, "h": {strings.Join(localAddresses(), ",")},
	}.Encode()}
	png, err := qrcode.Encode(link.String(), qrcode.Medium, 512)
	if err != nil {
		ui.notice("Pair a phone", err.Error())
		return
	}
	picture, _, err := image.Decode(bytes.NewReader(png))
	if err != nil {
		ui.notice("Pair a phone", err.Error())
		return
	}
	view := canvas.NewImageFromImage(picture)
	view.FillMode = canvas.ImageFillContain
	view.ScaleMode = canvas.ImageScalePixels
	view.SetMinSize(fyne.NewSize(240, 240))
	intro := widget.NewLabel("In the 1loop app on the phone, choose Remote desktop, then Scan QR code. The code works once, for two minutes.")
	intro.Wrapping = fyne.TextWrapWord
	hint := note("No camera? Pick this computer in the app's list and type the six digits it then shows here.")
	var dialog *sheet
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	dialog = ui.modal("Pair a phone", container.NewVBox(intro, spacer(0, 6), container.NewCenter(view), spacer(0, 6), hint), 460,
		func() { server.book.Cancel(pending.ID) }, cancel)
	server.showPairSheet(dialog)
}

// localAddresses are where a phone may reach this computer, for it to try
// in turn: first the address of the default route, then the other real
// networks; container and VM bridges are left out.
func localAddresses() []string {
	var out []string
	add := func(ip net.IP) {
		if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() && !slices.Contains(out, ip4.String()) && len(out) < 4 {
			out = append(out, ip4.String())
		}
	}
	// No packet is sent: connecting a UDP socket only picks the route.
	if probe, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		add(probe.LocalAddr().(*net.UDPAddr).IP)
		probe.Close()
	}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		virtual := false
		for _, prefix := range []string{"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "cni", "podman", "lxc", "lxd"} {
			virtual = virtual || strings.HasPrefix(iface.Name, prefix)
		}
		if virtual || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			if ip, ok := address.(*net.IPNet); ok {
				add(ip.IP)
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// State as the phone sees it

func (ui *desktopUI) chatByID(id string) *conversation {
	for _, chat := range ui.chats {
		if chat.ID == id {
			return chat
		}
	}
	return nil
}

// phoneChats are the chats the phone sees: this computer's. Those of SSH
// machines stay in the window.
func phoneChats(all []*conversation) []remoteapi.Chat {
	chats := []remoteapi.Chat{}
	for _, chat := range all {
		if chat.Config.MachineID == "" {
			chats = append(chats, chatSummary(chat))
		}
	}
	return chats
}

func chatSummary(chat *conversation) remoteapi.Chat {
	return remoteapi.Chat{ID: chat.ID, Title: chat.Title, Workspace: chat.Config.Workspace, Running: chat.run != nil, Items: len(chat.Items)}
}

func itemForPhone(chat *conversation, index int) remoteapi.Item {
	if chat.Items[index].ID == "" { // the whole chat: per item, a page cost its square
		chat.identifyMessages()
	}
	versions := chat.branching()
	item := chat.Items[index]
	out := remoteapi.Item{ID: item.ID, Versions: remoteapi.ItemVersions(&versions, index), Kind: item.Kind, Text: item.Text, Images: len(item.Images)}
	if item.Kind != "tool" {
		return out
	}
	state := runState(chat, index)
	verb, target, mono := agent.DescribeTool(item, state != "done")
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
	case item.Diff != "": // edit_file and apply_patch
		added, removed := diffStat(item.Diff)
		out.Meta, out.Tone = fmt.Sprintf("+%d −%d", added, removed), "added"
	case item.Tool == "shell" && item.Millis > 0:
		out.Meta, out.Tone = elapsed(item.Millis), "muted"
	}
	return out
}

func (ui *desktopUI) metaForPhone(chat *conversation) remoteapi.Meta {
	remoteMetaValue := remoteapi.Meta{Chat: chat.ID, Title: chat.Title, Running: chat.run != nil, Items: len(chat.Items), Queue: []string{}, Steer: []string{}}
	for _, message := range chat.queue {
		remoteMetaValue.Queue = append(remoteMetaValue.Queue, message.Content)
	}
	for _, messageValue := range chat.steer {
		remoteMetaValue.Steer = append(remoteMetaValue.Steer, messageValue.Content)
	}
	remoteMetaValue.Model, remoteMetaValue.Effort, remoteMetaValue.Tier = ui.choiceLabel(chat.Config), chat.Config.Effort, chat.Config.ServiceTier
	remoteMetaValue.Window = agent.ModelContextWindow(chat.Config)
	remoteMetaValue.Todo = todoForPhone(chat)
	remoteMetaValue.Tasks = append([]agent.BackgroundTaskInfo{}, chat.tasks.Snapshot()...)
	for _, h := range chat.History {
		remoteMetaValue.Used = max(remoteMetaValue.Used, h.Usage)
	}
	if run := chat.run; run != nil {
		if run.turn.usage > 0 {
			remoteMetaValue.Used = run.turn.usage
		}
		remoteMetaValue.Label, remoteMetaValue.Mark = run.turn.label, run.turn.mark
		if run.approvalID != "" {
			remoteMetaValue.Approval = &remoteapi.Approval{Chat: chat.ID, ID: run.approvalID, Title: run.approvalTitle, Preview: run.approvalPreview}
		}
		if run.questionID != "" {
			remoteMetaValue.Question = &remoteapi.Question{Chat: chat.ID, ID: run.questionID, Questions: run.questionList}
		}
	}
	return remoteMetaValue
}

func (server *remoteServer) state(writer http.ResponseWriter, _ *http.Request) {
	var out map[string]any
	server.onUI(func() {
		chats := phoneChats(server.ui.chats)
		current, workspace := "", server.ui.cfg.Workspace
		if server.ui.current != nil && server.ui.current.Config.MachineID == "" {
			current = server.ui.current.ID
		}
		if server.ui.cfg.MachineID != "" {
			workspace = server.ui.lastLocalWorkspace()
		}
		projects := append([]string{}, server.ui.projects...)
		out = map[string]any{"name": server.name, "projects": projects, "workspace": workspace,
			"projectManagement": true, "projectActions": true, "chats": chats, "current": current, "settings": server.ui.settingsForPhone()}
	})
	core.WriteJSON(writer, http.StatusOK, out)
}

func (server *remoteServer) chat(writer http.ResponseWriter, req *http.Request) {
	var out map[string]any
	server.onUI(func() {
		chat := server.ui.chatByID(req.PathValue("id"))
		if chat == nil {
			return
		}
		first, end := remoteapi.Page(req, len(chat.Items))
		items := make([]remoteapi.Item, 0, end-first)
		for i := first; i < end; i++ {
			items = append(items, itemForPhone(chat, i))
		}
		out = map[string]any{"chat": chatSummary(chat), "items": items, "first": first, "total": len(chat.Items), "meta": server.ui.metaForPhone(chat)}
	})
	if out == nil {
		core.Fail(writer, http.StatusNotFound, "This chat no longer exists")
		return
	}
	core.WriteJSON(writer, http.StatusOK, out)
}

func (server *remoteServer) deleteChat(writer http.ResponseWriter, req *http.Request) {
	found := false
	server.onUI(func() {
		chat := server.ui.chatByID(req.PathValue("id"))
		if chat == nil {
			return
		}
		found = true
		server.ui.removeChat(chat)
		delete(server.dirty, chat)
		delete(server.meta, chat)
	})
	if !found {
		core.Fail(writer, http.StatusNotFound, "This chat no longer exists")
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// clearBrowser is Settings → Browser → Clear, asked from the phone.
func (server *remoteServer) clearBrowser(writer http.ResponseWriter, _ *http.Request) {
	result := make(chan error, 1)
	server.onUI(func() { server.ui.clearBrowserData(func(err error) { result <- err }) })
	var err error
	select {
	case err = <-result:
	case <-time.After(30 * time.Second): // 1loop closing, or Chrome not answering
		err = fmt.Errorf("no answer from the browser")
	}
	if err != nil {
		core.Fail(writer, http.StatusInternalServerError, "Some data could not be cleared: "+err.Error())
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// itemImage serves a picture of an item, as the window shows it.
func (server *remoteServer) itemImage(writer http.ResponseWriter, req *http.Request) {
	remoteapi.ItemImage(writer, req, func(index int) (parts []core.ImagePart, ok bool) {
		server.onUI(func() {
			if chat := server.ui.chatByID(req.PathValue("id")); chat != nil && index >= 0 && index < len(chat.Items) {
				parts, ok = chat.Items[index].Images, true
			}
		})
		return parts, ok
	})
}

// stopTask is a background task's own Stop.
func (server *remoteServer) stopTask(writer http.ResponseWriter, req *http.Request) {
	found := false
	server.onUI(func() {
		if chat := server.ui.chatByID(req.PathValue("id")); chat != nil {
			found = chat.tasks.Stop(req.PathValue("task"))
		}
	})
	if !found {
		core.Fail(writer, http.StatusNotFound, "This task is not running")
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// Live events (server-sent events)

func (server *remoteServer) events(writer http.ResponseWriter, req *http.Request) {
	flusher, ok := writer.(http.Flusher)
	if !ok {
		core.Fail(writer, http.StatusInternalServerError, "Streaming unsupported")
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
		chats := phoneChats(server.ui.chats)
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
		Chat        string                 `json:"chat"`
		Text        string                 `json:"text"`
		Mode        string                 `json:"mode"`
		Workspace   string                 `json:"workspace"`
		Attachments []agent.FileAttachment `json:"attachments"`
	}
	if core.ReadJSON(req, &body) != nil || strings.TrimSpace(body.Text) == "" && len(body.Attachments) == 0 {
		core.Fail(writer, http.StatusBadRequest, "Nothing to send")
		return
	}
	root := body.Workspace
	server.onUI(func() {
		if chat := server.ui.chatByID(body.Chat); chat != nil {
			root = chat.Config.Workspace
		}
		if root == "" {
			root = server.ui.cfg.Workspace
		}
	})
	message, err := remoteapi.AttachedMessage(root, strings.TrimSpace(body.Text), body.Attachments)
	if err != nil {
		core.Fail(writer, 400, err.Error())
		return
	}
	var problem, chatID string
	server.onUI(func() {
		chatID, problem = server.ui.remoteSend(body.Chat, body.Mode, root, message)
	})
	if problem != "" {
		core.Fail(writer, http.StatusConflict, problem)
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]string{"chat": chatID})
}

// remoteSend sends a phone's message as if it had been typed in the window:
// the chat opens on the computer and the message is sent, queued or steered.
func (ui *desktopUI) remoteSend(chatID, mode, workspace string, messageValue agent.Message) (string, string) {
	if ui.server == nil && ui.cfg.MachineID != "" { // the phone works on this computer's chats
		ui.leaveMachine()
	}
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
			if root, err := agent.ResolveWorkspace(workspace); err == nil {
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
	core.ReadJSON(req, &body)
	server.onUI(func() {
		if chat := server.ui.chatByID(body.Chat); chat != nil && chat.run != nil {
			chat.run.cancel()
		}
	})
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// answer takes the phone's answers to the model's questions.
func (server *remoteServer) answer(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		ID      string            `json:"id"`
		Answers map[string]string `json:"answers"`
	}
	if core.ReadJSON(req, &body) != nil {
		core.Fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	found := false
	server.onUI(func() {
		for _, chat := range server.ui.chats {
			if run := chat.run; run != nil && run.questionID == body.ID && run.answerQuestions != nil {
				run.answerQuestions(body.Answers) // closes the questions on the computer too
				found = true
			}
		}
	})
	if !found {
		core.Fail(writer, http.StatusNotFound, "These questions were already answered")
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (server *remoteServer) approve(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		ID    string `json:"id"`
		Allow bool   `json:"allow"`
	}
	if core.ReadJSON(req, &body) != nil {
		core.Fail(writer, http.StatusBadRequest, "Invalid request")
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
		core.Fail(writer, http.StatusNotFound, "This request was already answered")
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// Models, reasoning effort and approval mode, as the composer offers them.

func (ui *desktopUI) choiceLabel(cfg agent.Config) string {
	for label, choice := range ui.choices {
		if choice.Connection.ID == cfg.ConnectionID && choice.ID == cfg.Model {
			return label
		}
	}
	return ""
}

func (ui *desktopUI) settingsForPhone() map[string]any {
	models := []remoteapi.Model{}
	for _, label := range ui.model.Options {
		choice := ui.choices[label]
		remoteModelValue := remoteapi.Model{ID: choice.ID, Label: label, Name: choice.Display(), Detail: ui.model.Details[label],
			Account: choice.Connection.ID, Family: agent.FamilyOf(choice).Name, Efforts: []remoteapi.Effort{}, ServiceTiers: choice.ServiceTiers, DefaultServiceTier: choice.DefaultServiceTier}
		for _, connection := range ui.connectionsList {
			if connection.ID == choice.Connection.ID {
				remoteModelValue.CompactAt = connection.ModelOptions[choice.ID].CompactAt
				cfg := connection.Config("")
				cfg.Model = choice.ID
				remoteModelValue.CompactDefault, _ = agent.CompactDefault(cfg)
				remoteModelValue.CompactHelp = agent.CompactHelp(cfg)
				remoteModelValue.CompactSupported = connection.Provider != "chatgptweb"
				break
			}
		}
		for _, e := range choice.Efforts {
			remoteModelValue.Efforts = append(remoteModelValue.Efforts, remoteapi.Effort{Value: e, Label: agent.EffortLabel(e), About: modelEffortAbout(e)})
		}
		models = append(models, remoteModelValue)
	}
	accounts := []remoteapi.Account{}
	for _, conn := range ui.connectionsList {
		if conn.Enabled {
			accounts = append(accounts, remoteapi.Account{ID: conn.ID, Name: conn.Name, Provider: conn.Provider, Kind: agent.ProviderName(conn.Provider), Problem: ui.modelUnavailable[conn.ID]})
		}
	}
	return map[string]any{"autoResume": ui.autoResume, "userPreferences": ui.userInstructions, "preferencesEditable": true, "memory": !ui.memoryOff, "messageVersions": true, "attachments": true, "models": models, "accounts": accounts, "model": ui.choiceLabel(ui.cfg), "effort": ui.cfg.Effort, "tier": ui.cfg.ServiceTier,
		"approvalMode": agent.ApprovalFor(ui.approvalMode, ui.cfg), "autoMode": agent.AutoModeOffered(ui.cfg), "loading": ui.modelsLoading, "window": agent.ModelContextWindow(ui.cfg)}
}

// limits reads every account's usage windows for the phone's model picker,
// with the desktop menu's cache (a minute).
func (server *remoteServer) limits(writer http.ResponseWriter, req *http.Request) {
	var accounts []agent.Connection
	keys := map[string]string{}
	cached := map[string]agent.AccountLimits{}
	server.onUI(func() {
		for _, conn := range server.ui.connectionsList {
			if !conn.Enabled {
				continue
			}
			accounts = append(accounts, conn)
			keys[conn.ID] = server.ui.keys[conn.ID]
			if limits, ok := server.ui.accountLimits[conn.ID]; ok && time.Since(limits.Checked) < time.Minute {
				cached[conn.ID] = limits
			}
		}
	})
	results := make([]agent.AccountLimits, len(accounts))
	var wg sync.WaitGroup
	for i, conn := range accounts {
		if limits, ok := cached[conn.ID]; ok {
			results[i] = limits
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			limits, err := server.ui.backend.ConnectionLimits(req.Context(), conn, keys[conn.ID])
			if err != nil {
				limits = agent.AccountLimits{Note: "Limits unavailable"}
			}
			limits.Checked = time.Now()
			results[i] = limits
		}()
	}
	wg.Wait()
	out := []remoteapi.Limits{}
	server.onUI(func() {
		if server.ui.accountLimits == nil {
			server.ui.accountLimits = map[string]agent.AccountLimits{}
		}
		for i, conn := range accounts {
			server.ui.accountLimits[conn.ID] = results[i]
			value := remoteapi.Limits{Account: conn.ID, Note: results[i].Note, Windows: []remoteapi.LimitWindow{}}
			for _, window := range results[i].Windows {
				reset := int64(0)
				if !window.ResetAt.IsZero() {
					reset = window.ResetAt.UnixMilli()
				}
				value.Windows = append(value.Windows, remoteapi.LimitWindow{Label: window.Label, Left: window.Remaining(), Reset: reset})
			}
			out = append(out, value)
		}
	})
	core.WriteJSON(writer, http.StatusOK, map[string]any{"accounts": out})
}

func (server *remoteServer) setAutoResume(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Policy agent.AutoResume `json:"policy"`
	}
	if core.ReadJSON(req, &body) != nil {
		core.Fail(writer, 400, "Invalid request")
		return
	}
	if err := body.Policy.Validate(); err != nil {
		core.Fail(writer, 400, err.Error())
		return
	}
	problem := ""
	server.onUI(func() {
		for _, step := range body.Policy.Steps {
			if step.Action != "switch" {
				continue
			}
			found := false
			for _, choice := range server.ui.choices {
				if choice.Connection.ID == step.ConnectionID && choice.ID == step.Model && choice.Connection.Enabled {
					found = true
					break
				}
			}
			if !found {
				problem = "Resume model is unavailable"
				return
			}
		}
		server.ui.autoResume = &body.Policy
		server.ui.save()
		server.settingsChanged()
	})
	if problem != "" {
		core.Fail(writer, 400, problem)
		return
	}
	core.WriteJSON(writer, 200, map[string]bool{"ok": true})
}

func (server *remoteServer) setModelCompaction(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Label string `json:"label"`
		Limit int    `json:"limit"`
	}
	if core.ReadJSON(req, &body) != nil {
		core.Fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	problem := "Connection not found"
	server.onUI(func() {
		choice, ok := server.ui.choices[body.Label]
		if !ok {
			problem = "Unknown model"
			return
		}
		for i := range server.ui.connectionsList {
			connection := &server.ui.connectionsList[i]
			if connection.ID != choice.Connection.ID {
				continue
			}
			if err := connection.SetCompactLimit(choice.ID, body.Limit); err != nil {
				problem = err.Error()
				return
			}
			problem = ""
			server.ui.save()
			server.settingsChanged()
			return
		}
	})
	if problem != "" {
		core.Fail(writer, http.StatusBadRequest, problem)
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (server *remoteServer) setModel(writer http.ResponseWriter, req *http.Request) {
	var body struct {
		Chat   string  `json:"chat"`
		Label  string  `json:"label"`
		Effort string  `json:"effort"`
		Tier   *string `json:"tier"` // a speed; "" for the plan's default
	}
	if core.ReadJSON(req, &body) != nil {
		core.Fail(writer, http.StatusBadRequest, "Invalid request")
		return
	}
	problem := ""
	server.onUI(func() { problem = server.ui.remoteModel(body.Chat, body.Label, body.Effort, body.Tier) })
	if problem != "" {
		core.Fail(writer, http.StatusConflict, problem)
		return
	}
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// remoteModel picks a chat's model and effort from the phone, through the
// same picker as the window (the chat opens there to show it).
func (ui *desktopUI) remoteModel(chatID, label, effort string, tier *string) string {
	if ui.server == nil && ui.cfg.MachineID != "" { // the phone works on this computer's chats
		ui.leaveMachine()
	}
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
		if !ok || !agent.Contains(choice.Efforts, effort) {
			return "This model has no such reasoning level"
		}
		ui.cfg.Effort = effort
		if ui.current != nil {
			ui.current.Config.Effort = effort
		}
		ui.model.row.SetTrailing(modelTrailing(choice, ui.cfg))
		if ui.model.relayout != nil {
			ui.model.relayout()
		}
		ui.save()
		ui.remote.settingsChanged()
		ui.remote.metaChanged(ui.current)
	}
	if tier != nil {
		choice, ok := ui.selectedChoice()
		if !ok || *tier != "" && agent.TierFor(choice, *tier) != *tier {
			return "This model has no such speed"
		}
		ui.cfg.ServiceTier = *tier
		if ui.current != nil {
			ui.current.Config.ServiceTier = *tier
		}
		ui.model.row.SetTrailing(modelTrailing(choice, ui.cfg))
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
	if core.ReadJSON(req, &body) != nil || (body.Mode != "approval" && body.Mode != "yolo" && body.Mode != "auto") {
		core.Fail(writer, http.StatusBadRequest, "Invalid mode")
		return
	}
	server.onUI(func() {
		server.ui.approvalMode = body.Mode
		server.ui.updateApprovalControl()
		server.ui.save()
	})
	core.WriteJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------------------
// Settings: the remote switch and the paired phones.

func (ui *desktopUI) remoteSettings() fyne.CanvasObject {
	describe := func() string {
		switch {
		case ui.remote != nil:
			return fmt.Sprintf("Listening on port %d as “%s” (HTTPS). A phone must be paired with a QR code or a code shown on this screen.", ui.remote.port, ui.remote.name)
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
			if ui.remote != nil {
				ui.remote.disconnectAll()
			}
			ui.prefs.show("Phone remote")
		})
		phones = append(phones, settingRow(pairedDevice.Name, "Paired "+pairedDevice.Added.Format("2 Jan 2006 15:04"), remove))
	}
	if len(phones) == 0 {
		phones = append(phones, settingRow("No paired phone yet", "Open the 1loop app on a phone connected to the same network.", nil))
	}
	var pairing fyne.CanvasObject = layout.NewSpacer()
	if ui.remote != nil {
		pairing = card(settingRow("Pair a phone", "Shows a QR code to scan with the 1loop app.", newRow("Pair a phone", iconPhone, styleChip, ui.showPairingQR)))
	}
	return container.NewVBox(
		ui.prefs.pageTitle("Phone remote", "", nil),
		card(settingRow("Allow the 1loop phone app on this network", describe(), enabled)),
		pairing,
		sectionTitle("Paired phones"),
		card(phones...),
	)
}

// remoteDefaults gives conversations saved before the remote an id.
func (ui *desktopUI) remoteDefaults() {
	for _, chat := range ui.chats {
		if chat.ID == "" {
			chat.ID = core.Identifier()[:16]
		}
	}
}
