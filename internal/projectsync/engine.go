package projectsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"1loop/internal/core"
)

// Engine is one side of a copy: this process, or a paired engine reached
// over HTTPS. project is a project's folder there, or, for a project that
// may not exist yet, its name.
type Engine interface {
	ID() string
	Manifest(ctx context.Context, project string) (Manifest, error)
	Export(ctx context.Context, project string, plan Plan, w io.Writer) error
	Import(ctx context.Context, project string, r io.Reader) (Result, string, error)
}

// Local is this process's engine: the desktop's or a server's projects and
// chats, through what it gives here.
type Local struct {
	Identity string
	Bases    *BaseStore
	Device   string                                                           // shown in conflict names
	Resolve  func(project string, create bool) (string, error)                // a project's folder
	Chats    func(workspace string) []Chat                                    // its conversations
	AddChat  func(workspace string, chat Chat, conflict bool) (string, error) // stores one; returns its title
	Saved    func() error                                                     // after an import
}

func (local *Local) ID() string {
	if local.Identity != "" {
		return local.Identity
	}
	return "local"
}

func (local *Local) Manifest(_ context.Context, project string) (Manifest, error) {
	workspace, err := local.Resolve(project, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{Files: map[string]FileState{}, Chats: map[string]ChatState{}}, nil
		}
		return Manifest{}, err
	}
	return MakeManifest(workspace, local.Chats(workspace))
}

func (local *Local) Export(_ context.Context, project string, plan Plan, w io.Writer) error {
	workspace, err := local.Resolve(project, false)
	if err != nil {
		return err
	}
	return Export(w, workspace, local.Device, plan, local.Chats(workspace))
}

func (local *Local) Import(_ context.Context, project string, r io.Reader) (Result, string, error) {
	workspace, err := local.Resolve(project, true)
	if err != nil {
		return Result{}, "", err
	}
	result, err := Import(r, workspace, TrashDir(), func(chat Chat, conflict bool) (string, error) {
		return local.AddChat(workspace, chat, conflict)
	})
	if local.Saved != nil {
		if saveErr := local.Saved(); err == nil {
			err = saveErr
		}
	}
	return result, workspace, err
}

type peerError struct {
	status  int
	problem string
}

func (err *peerError) Error() string { return err.problem }

// Peer is a paired engine: its address, the device token it gave, and its
// certificate's fingerprint.
type Peer struct {
	URL         string `json:"url"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
}

func (peer *Peer) ID() string {
	if peer.Fingerprint != "" {
		return strings.ToLower(strings.ReplaceAll(peer.Fingerprint, ":", ""))
	}
	return peer.URL
}

func (peer *Peer) client() (*http.Client, error) {
	settings := &tls.Config{MinVersion: tls.VersionTLS12}
	if peer.Fingerprint != "" {
		expected, err := hex.DecodeString(strings.ToLower(strings.ReplaceAll(peer.Fingerprint, ":", "")))
		if err != nil || len(expected) != sha256.Size {
			return nil, fmt.Errorf("Invalid certificate fingerprint")
		}
		// The certificate the user paired with, exactly; never any other.
		settings.InsecureSkipVerify = true
		settings.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return fmt.Errorf("The engine sent no certificate")
			}
			actual := sha256.Sum256(state.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(actual[:], expected) != 1 {
				return fmt.Errorf("The engine's certificate changed: pair it again")
			}
			return nil
		}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: settings, Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("The paired engine redirected the request")
	}}, nil
}

func (peer *Peer) request(ctx context.Context, method, path string, body io.Reader, kind string) (*http.Response, error) {
	address, parseErr := url.Parse(peer.URL)
	if parseErr != nil || address.Scheme != "https" || address.Host == "" || address.User != nil {
		return nil, fmt.Errorf("A paired HTTPS engine is required")
	}
	client, err := peer.client()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(peer.URL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+peer.Token)
	if kind != "" {
		req.Header.Set("Content-Type", kind)
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		var problem struct{ Error string }
		json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&problem)
		if problem.Error == "" {
			problem.Error = response.Status
		}
		return nil, &peerError{status: response.StatusCode, problem: problem.Error}
	}
	return response, nil
}

func (peer *Peer) Manifest(ctx context.Context, project string) (Manifest, error) {
	response, err := peer.request(ctx, "GET", "/api/sync/manifest?project="+url.QueryEscape(project), nil, "")
	if err != nil {
		return Manifest{}, err
	}
	defer response.Body.Close()
	var manifest Manifest
	err = json.NewDecoder(response.Body).Decode(&manifest)
	return manifest, err
}

func (peer *Peer) Export(ctx context.Context, project string, plan Plan, w io.Writer) error {
	body, _ := json.Marshal(plan)
	response, err := peer.request(ctx, "POST", "/api/sync/export?project="+url.QueryEscape(project), bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, err = io.Copy(w, response.Body)
	return err
}

func (peer *Peer) Import(ctx context.Context, project string, r io.Reader) (Result, string, error) {
	response, err := peer.request(ctx, "POST", "/api/sync/import?project="+url.QueryEscape(project), r, "application/x-tar")
	if err != nil {
		return Result{}, "", err
	}
	defer response.Body.Close()
	var reply struct {
		Result    Result `json:"result"`
		Workspace string `json:"workspace"`
	}
	err = json.NewDecoder(response.Body).Decode(&reply)
	return reply.Result, reply.Workspace, err
}

type checkpointEngine interface {
	ReadBase(context.Context, string, string, string) (Base, error)
	SaveBase(context.Context, string, string, string, Base) error
}

func (local *Local) ReadBase(_ context.Context, project, peerID, peerWorkspace string) (Base, error) {
	if local.Bases == nil {
		return Base{}, nil
	}
	workspace, err := local.Resolve(project, false)
	if err != nil {
		return Base{}, err
	}
	return local.Bases.Get(local.ID(), workspace, peerID, peerWorkspace), nil
}

func (local *Local) SaveBase(_ context.Context, project, peerID, peerWorkspace string, base Base) error {
	if local.Bases == nil {
		return nil
	}
	workspace, err := local.Resolve(project, false)
	if err != nil {
		return err
	}
	return local.Bases.Put(local.ID(), workspace, peerID, peerWorkspace, base)
}

func (peer *Peer) ReadBase(ctx context.Context, project, otherID, otherWorkspace string) (Base, error) {
	response, err := peer.request(ctx, "GET", "/api/sync/base?project="+url.QueryEscape(project)+"&peer="+url.QueryEscape(otherID)+"&workspace="+url.QueryEscape(otherWorkspace), nil, "")
	if err != nil {
		return Base{}, err
	}
	defer response.Body.Close()
	var base Base
	err = json.NewDecoder(response.Body).Decode(&base)
	return base, err
}

func (peer *Peer) SaveBase(ctx context.Context, project, otherID, otherWorkspace string, base Base) error {
	body, _ := json.Marshal(base)
	response, err := peer.request(ctx, "POST", "/api/sync/base?project="+url.QueryEscape(project)+"&peer="+url.QueryEscape(otherID)+"&workspace="+url.QueryEscape(otherWorkspace), bytes.NewReader(body), "application/json")
	if err != nil {
		var unavailable *peerError
		if errors.As(err, &unavailable) && unavailable.status == http.StatusNotFound {
			return nil
		} // older engines lack checkpoints
		return err
	}
	response.Body.Close()
	return nil
}

// BaseRoute exposes only the checkpoint for an authorized, registered project.
func (local *Local) BaseRoute(w http.ResponseWriter, r *http.Request) {
	project, peer, workspace := r.URL.Query().Get("project"), r.URL.Query().Get("peer"), r.URL.Query().Get("workspace")
	if r.Method == "GET" {
		base, err := local.ReadBase(r.Context(), project, peer, workspace)
		if err != nil {
			core.Fail(w, 400, err.Error())
			return
		}
		core.WriteJSON(w, 200, base)
		return
	}
	var base Base
	if core.ReadJSON(r, &base) != nil {
		core.Fail(w, 400, "Invalid copy checkpoint")
		return
	}
	if err := local.SaveBase(r.Context(), project, peer, workspace, base); err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, map[string]bool{"saved": true})
}

// Routes serve a local engine to its paired peers: GET /api/sync/manifest,
// POST /api/sync/export and POST /api/sync/import, all ?project=.
func (local *Local) Routes() (manifest, export, importing http.HandlerFunc) {
	manifest = func(w http.ResponseWriter, r *http.Request) {
		m, err := local.Manifest(r.Context(), r.URL.Query().Get("project"))
		if err != nil {
			core.Fail(w, 500, err.Error())
			return
		}
		core.WriteJSON(w, 200, m)
	}
	export = func(w http.ResponseWriter, r *http.Request) {
		var plan Plan
		if core.ReadJSON(r, &plan) != nil {
			core.Fail(w, 400, "Invalid copy plan")
			return
		}
		workspace, err := local.Resolve(r.URL.Query().Get("project"), false)
		if err != nil {
			core.Fail(w, 404, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/x-tar")
		if err := Export(w, workspace, local.Device, plan, local.Chats(workspace)); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	importing = func(w http.ResponseWriter, r *http.Request) {
		result, workspace, err := local.Import(r.Context(), r.URL.Query().Get("project"), r.Body)
		if err != nil {
			core.Fail(w, 400, err.Error())
			return
		}
		core.WriteJSON(w, 200, map[string]any{"result": result, "workspace": workspace})
	}
	return manifest, export, importing
}

// Report is a copy, done or only planned.
type Report struct {
	Plan      Plan   `json:"plan"`
	Result    Result `json:"result"`
	Workspace string `json:"workspace"` // the project's folder on the target
	Done      bool   `json:"done"`
}

// Copy copies a project from one engine to another. With plan only, it
// says what a copy would do. bases keeps, per pair of projects, what they
// shared after the previous copy.
func Copy(ctx context.Context, from Engine, fromProject string, to Engine, toProject string, bases *BaseStore, planOnly bool) (Report, error) {
	source, err := from.Manifest(ctx, fromProject)
	if err != nil {
		return Report{}, err
	}
	if source.Workspace == "" {
		return Report{}, fmt.Errorf("This project does not exist on the source")
	}
	target, err := to.Manifest(ctx, toProject)
	if err != nil {
		return Report{}, err
	}
	base := Base{}
	if target.Workspace != "" {
		base = bases.Get(from.ID(), source.Workspace, to.ID(), target.Workspace)
	}
	if len(base.Files)+len(base.Chats) == 0 && target.Workspace != "" {
		if checkpoints, ok := from.(checkpointEngine); ok {
			if saved, readErr := checkpoints.ReadBase(ctx, source.Workspace, to.ID(), target.Workspace); readErr == nil {
				base = saved
			}
		}
		if len(base.Files)+len(base.Chats) == 0 {
			if checkpoints, ok := to.(checkpointEngine); ok {
				if saved, readErr := checkpoints.ReadBase(ctx, target.Workspace, from.ID(), source.Workspace); readErr == nil {
					base = saved
				}
			}
		}
	}
	plan := MakePlan(source, target, base)
	report := Report{Plan: plan, Workspace: target.Workspace}
	if planOnly {
		return report, nil
	}
	reader, writer := io.Pipe()
	exported := make(chan error, 1)
	go func() {
		err := from.Export(ctx, source.Workspace, plan, writer)
		writer.CloseWithError(err)
		exported <- err
	}()
	result, workspace, err := to.Import(ctx, toProject, reader)
	reader.CloseWithError(io.ErrClosedPipe)
	if exportErr := <-exported; err == nil && exportErr != nil && exportErr != io.ErrClosedPipe {
		err = exportErr
	}
	if err != nil {
		return report, err
	}
	report.Result, report.Workspace, report.Done = result, workspace, true
	nextBase := BaseAfter(source, target, base, plan)
	if err := bases.Put(from.ID(), source.Workspace, to.ID(), workspace, nextBase); err != nil {
		return report, fmt.Errorf("Copied, but could not save the local checkpoint: %w", err)
	}
	if checkpoints, ok := from.(checkpointEngine); ok {
		if err := checkpoints.SaveBase(ctx, source.Workspace, to.ID(), workspace, nextBase); err != nil {
			return report, fmt.Errorf("Copied, but could not save the source checkpoint: %w", err)
		}
	}
	if checkpoints, ok := to.(checkpointEngine); ok {
		if err := checkpoints.SaveBase(ctx, workspace, from.ID(), source.Workspace, nextBase); err != nil {
			return report, fmt.Errorf("Copied, but could not save the destination checkpoint: %w", err)
		}
	}
	return report, nil
}

// BaseStore keeps the bases in a file, keyed by the pair of projects
// whichever way the copy goes.
type BaseStore struct{ Path string }

var baseFileMu sync.Mutex

func pairKey(a, aWorkspace, b, bWorkspace string) string {
	pair := []string{a + "|" + aWorkspace, b + "|" + bWorkspace}
	sort.Strings(pair)
	return pair[0] + "||" + pair[1]
}

func (store *BaseStore) read() map[string]Base {
	all := map[string]Base{}
	if data, err := os.ReadFile(store.Path); err == nil {
		json.Unmarshal(data, &all)
	}
	return all
}

func (store *BaseStore) Get(a, aWorkspace, b, bWorkspace string) Base {
	baseFileMu.Lock()
	defer baseFileMu.Unlock()
	return store.read()[pairKey(a, aWorkspace, b, bWorkspace)]
}

func (store *BaseStore) Put(a, aWorkspace, b, bWorkspace string, base Base) error {
	baseFileMu.Lock()
	defer baseFileMu.Unlock()
	all := store.read()
	all[pairKey(a, aWorkspace, b, bWorkspace)] = base
	data, _ := json.Marshal(all)
	if err := os.MkdirAll(filepath.Dir(store.Path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(store.Path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(store.Path+".tmp", store.Path)
}

// Summary says in a line what a plan does, or did.
func (report Report) Summary() string {
	plan := report.Plan
	if plan.Empty() {
		return "Nothing to copy from this side. Changes made only on the destination are kept."
	}
	parts := []string{}
	if n := len(plan.Copy); n > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s)", n))
	}
	if n := len(plan.Chats); n > 0 {
		parts = append(parts, fmt.Sprintf("%d conversation(s)", n))
	}
	if n := len(plan.Delete); n > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted file(s) to the trash", n))
	}
	if n := len(plan.Conflicts) + len(plan.ChatConflicts); n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed on both sides, kept twice", n))
	}
	text := strings.Join(parts, ", ") + fmt.Sprintf(" · %s of file data", size(plan.Bytes))
	if len(plan.Kept) > 0 {
		text += fmt.Sprintf(" · %d file(s) changed only on the target stay as they are", len(plan.Kept))
	}
	return text
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
