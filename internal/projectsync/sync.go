// Package projectsync copies a project — its files and its conversations —
// from one 1loop engine to another (the desktop, a server, a phone), and
// back. Each copy sends only what changed since the last one: both sides'
// manifests are compared with the state the previous copy left (the base).
// What changed on both sides is kept twice, never overwritten; a file
// deleted on one side goes to the other side's trash, never straight away.
package projectsync

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// Chat is a conversation as it travels between engines.
type Chat struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Config   agent.Config        `json:"config"`
	History  []agent.Message     `json:"history"`
	Items    []agent.ChatItem    `json:"items"`
	Todo     *agent.TodoState    `json:"todo,omitempty"`
	Version  string              `json:"version,omitempty"`
	Versions []agent.ChatVersion `json:"versions,omitempty"`
}

// Revision identifies a chat's content: what the user and the models wrote.
func (chat Chat) Revision() string {
	chat = chat.Portable()
	for i := range chat.Versions {
		if chat.Versions[i].Snapshot != nil {
			chat.Versions[i].Snapshot.Config = agent.Config{}
		}
	}
	data, _ := json.Marshal([]any{chat.Title, chat.History, chat.Items, chat.Todo, chat.Version, chat.Versions})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:12])
}

const relocationPrefix = "[Project copied between devices]"

// Portable returns an independent snapshot: account settings and CLI sessions
// belong to their device, while messages, tool results, images and TODO travel.
func (chat Chat) Portable() Chat {
	data, _ := json.Marshal(chat)
	var copied Chat
	_ = json.Unmarshal(data, &copied)
	cleanConfig := func(cfg agent.Config) agent.Config {
		identity := agent.IdentityOf(cfg)
		if cfg.RanWith != nil {
			identity = *cfg.RanWith
		}
		identity.AuthProfile = ""
		return agent.Config{Provider: cfg.Provider, Model: cfg.Model, Workspace: cfg.Workspace, RanWith: &identity}
	}
	cleanHistory := func(history []agent.Message) []agent.Message {
		out := history[:0]
		for _, message := range history {
			if message.Internal && strings.HasPrefix(message.Content, relocationPrefix) {
				continue
			}
			message.Usage, message.CachedTokens, message.UsageEstimated = 0, nil, false
			var responses []json.RawMessage
			for _, raw := range message.Responses {
				var item struct{ Type string }
				if json.Unmarshal(raw, &item) == nil && item.Type != "reasoning" {
					responses = append(responses, raw)
				}
			}
			message.Responses = responses
			out = append(out, message)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	cleanItems := func(items []agent.ChatItem) []agent.ChatItem {
		out := items[:0]
		for _, item := range items {
			if item.Kind != "note" || !strings.HasPrefix(item.Text, relocationPrefix) {
				out = append(out, item)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	copied.Config = cleanConfig(copied.Config)
	copied.History, copied.Items = cleanHistory(copied.History), cleanItems(copied.Items)
	for i := range copied.Versions {
		if snapshot := copied.Versions[i].Snapshot; snapshot != nil {
			snapshot.Config = cleanConfig(snapshot.Config)
			snapshot.History, snapshot.Items = cleanHistory(snapshot.History), cleanItems(snapshot.Items)
		}
	}
	return copied
}

// AtWorkspace keeps original user messages verbatim and explains how their
// older paths map to the destination; no foreign session or account is resumed.
func (chat Chat) AtWorkspace(cfg agent.Config) Chat {
	from := chat.Config.Workspace
	chat.Config = cfg
	chat.Config.Session, chat.Config.SessionMark = "", 0
	if from != cfg.Workspace {
		note := fmt.Sprintf("%s Work now takes place in %s. Earlier absolute paths under %s refer to this project's files there. Use this destination's accounts and tools; keep work within the current project.", relocationPrefix, cfg.Workspace, from)
		chat.History = append(chat.History, agent.Message{Role: "user", Internal: true, Content: note})
		chat.Items = append(chat.Items, agent.ChatItem{Kind: "note", Text: note})
	}
	for i := range chat.Versions {
		if snapshot := chat.Versions[i].Snapshot; snapshot != nil {
			snapshot.Config = chat.Config
		}
	}
	return chat
}

type FileState struct {
	Size int64  `json:"size"`
	Hash string `json:"hash"`
	Mode uint32 `json:"mode"`
}

type ChatState struct {
	Title    string `json:"title"`
	Revision string `json:"revision"`
}

// Manifest is a project's state on one engine.
type Manifest struct {
	Workspace string               `json:"workspace"` // empty when the project does not exist there yet
	Files     map[string]FileState `json:"files"`
	Chats     map[string]ChatState `json:"chats"`
}

// heavy folders are rebuilt by tools, never copied outside a Git project
// (where .gitignore decides).
var heavy = map[string]bool{"node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".gradle": true, ".mypy_cache": true, ".pytest_cache": true}

// excluded keeps runtime/account material and local secrets on their device.
func excluded(relative string) bool {
	for _, component := range strings.Split(filepath.ToSlash(relative), "/") {
		lower := strings.ToLower(component)
		if component == ".home" || heavy[component] || lower == ".ssh" || lower == ".claude" || lower == ".codex" || lower == ".aws" {
			return true
		}
		if lower == ".env" || strings.HasPrefix(lower, ".env.") && !strings.HasSuffix(lower, ".example") && !strings.HasSuffix(lower, ".sample") {
			return true
		}
		if lower == "token" || lower == "id_rsa" || lower == "id_ed25519" || lower == ".credentials.json" || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx") || strings.HasSuffix(lower, ".key") {
			return true
		}
	}
	return strings.HasSuffix(relative, ".lock") && strings.HasPrefix(relative, ".git/")
}

// maxFile is the largest file a copy carries.
const maxFile = 512 << 20

// ScanFiles lists a project's files with their hashes. In a Git project the
// files Git sees (tracked or not ignored) and the repository itself are
// copied; elsewhere everything but the heavy folders.
func ScanFiles(root string) (map[string]FileState, error) {
	files := map[string]FileState{}
	add := func(relative string) error {
		relative = filepath.ToSlash(relative)
		if relative == "" || excluded(relative) {
			return nil // .home is the phone's own home for the project's tools
		}
		full, err := inside(root, relative)
		if err != nil {
			return err
		}
		info, err := os.Lstat(full)
		if os.IsNotExist(err) {
			return nil
		} // tracked files deleted in the working tree
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() > maxFile {
			return fmt.Errorf("%s is larger than the 512 MB copy limit", relative)
		}
		hash, err := fileHash(full)
		if err != nil {
			return err
		}
		files[relative] = FileState{Size: info.Size(), Hash: hash, Mode: uint32(info.Mode().Perm())}
		return nil
	}
	if info, err := os.Stat(filepath.Join(root, ".git")); err == nil && info.IsDir() {
		out, err := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
		if err == nil {
			for _, name := range strings.Split(string(out), "\x00") {
				if err := add(name); err != nil {
					return nil, err
				}
			}
			err = filepath.WalkDir(filepath.Join(root, ".git"), func(full string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !entry.IsDir() {
					rel, _ := filepath.Rel(root, full)
					return add(rel)
				}
				return nil
			})
			return files, err
		}
	}
	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			rel, _ := filepath.Rel(root, full)
			if full != root && excluded(filepath.ToSlash(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, full)
		return add(rel)
	})
	return files, err
}

func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// MakeManifest is a project's manifest with its chats.
func MakeManifest(root string, chats []Chat) (Manifest, error) {
	manifest := Manifest{Workspace: root, Files: map[string]FileState{}, Chats: map[string]ChatState{}}
	if root == "" {
		return manifest, nil
	}
	files, err := ScanFiles(root)
	if err != nil {
		return manifest, err
	}
	manifest.Files = files
	for _, chat := range chats {
		manifest.Chats[chat.ID] = ChatState{Title: chat.Title, Revision: chat.Revision()}
	}
	return manifest, nil
}

// Base is what both sides shared after the previous copy.
type Base struct {
	Files map[string]string `json:"files"` // path -> hash
	Chats map[string]string `json:"chats"` // id -> revision
}

// Plan is what a copy from source to target does.
type Plan struct {
	Copy          []string          `json:"copy"`          // new or changed on the source only
	Conflicts     []string          `json:"conflicts"`     // changed on both sides: both kept
	Delete        []string          `json:"delete"`        // deleted on the source, unchanged on the target: to its trash
	Kept          []string          `json:"kept"`          // changed on the target only: left as they are
	Chats         []string          `json:"chats"`         // new or changed on the source only
	ChatConflicts []string          `json:"chatConflicts"` // changed on both: the source's arrives as a copy
	Bytes         int64             `json:"bytes"`
	SourceHashes  map[string]string `json:"sourceHashes,omitempty"`
	TargetHashes  map[string]string `json:"targetHashes,omitempty"`
}

func (plan Plan) Empty() bool {
	return len(plan.Copy)+len(plan.Conflicts)+len(plan.Delete)+len(plan.Chats)+len(plan.ChatConflicts) == 0
}

// MakePlan compares both sides with the base, path by path.
func MakePlan(source, target Manifest, base Base) Plan {
	var plan Plan
	paths := map[string]bool{}
	for p := range source.Files {
		paths[p] = true
	}
	for p := range target.Files {
		paths[p] = true
	}
	for p := range base.Files {
		paths[p] = true
	}
	for p := range paths {
		s, inSource := source.Files[p]
		t, inTarget := target.Files[p]
		b, inBase := base.Files[p]
		switch {
		case inSource && inTarget && s.Hash == t.Hash:
		case inSource && !inTarget:
			if inBase && b == s.Hash {
				continue // deleted on the target since the last copy: the target's choice stands
			}
			plan.Copy = append(plan.Copy, p)
			plan.Bytes += s.Size
		case inSource && inTarget:
			switch {
			case inBase && t.Hash == b: // the target did not change it
				plan.Copy = append(plan.Copy, p)
				plan.Bytes += s.Size
			case inBase && s.Hash == b: // only the target changed it
				plan.Kept = append(plan.Kept, p)
			default:
				plan.Conflicts = append(plan.Conflicts, p)
				plan.Bytes += s.Size
			}
		case !inSource && inTarget:
			if inBase && t.Hash == b {
				plan.Delete = append(plan.Delete, p)
			} else if inBase {
				plan.Kept = append(plan.Kept, p) // deleted here, changed there: kept
			}
			// Never shared: a file of the target only.
		}
	}
	for id, s := range source.Chats {
		t, inTarget := target.Chats[id]
		b, inBase := base.Chats[id]
		switch {
		case !inTarget:
			if !(inBase && b == s.Revision) { // not one the target deleted
				plan.Chats = append(plan.Chats, id)
			}
		case s.Revision == t.Revision:
		case inBase && t.Revision == b:
			plan.Chats = append(plan.Chats, id)
		case inBase && s.Revision == b:
		default:
			plan.ChatConflicts = append(plan.ChatConflicts, id)
		}
	}
	for _, list := range [][]string{plan.Copy, plan.Conflicts, plan.Delete, plan.Kept, plan.Chats, plan.ChatConflicts} {
		sort.Strings(list)
	}
	plan.SourceHashes, plan.TargetHashes = map[string]string{}, map[string]string{}
	for _, paths := range [][]string{plan.Copy, plan.Conflicts, plan.Delete} {
		for _, p := range paths {
			plan.SourceHashes[p] = source.Files[p].Hash
			plan.TargetHashes[p] = target.Files[p].Hash
		}
	}
	return plan
}

// BaseAfter is what both sides share once the plan is applied. What the
// target alone changed keeps the old base, so the next copy, either way,
// still knows which side changed it.
func BaseAfter(source, target Manifest, base Base, plan Plan) Base {
	next := Base{Files: map[string]string{}, Chats: map[string]string{}}
	in := func(list []string) map[string]bool {
		set := map[string]bool{}
		for _, item := range list {
			set[item] = true
		}
		return set
	}
	copied, conflicts, kept := in(plan.Copy), in(plan.Conflicts), in(plan.Kept)
	for p, s := range source.Files {
		t, inTarget := target.Files[p]
		switch {
		case copied[p] || conflicts[p] || inTarget && t.Hash == s.Hash:
			next.Files[p] = s.Hash
		case kept[p] || !inTarget:
			if b, ok := base.Files[p]; ok {
				next.Files[p] = b
			}
		}
	}
	for p := range target.Files {
		if _, inSource := source.Files[p]; !inSource && kept[p] {
			if b, ok := base.Files[p]; ok {
				next.Files[p] = b
			}
		}
	}
	chats, chatConflicts := in(plan.Chats), in(plan.ChatConflicts)
	for id, s := range source.Chats {
		t, inTarget := target.Chats[id]
		switch {
		case chats[id] || chatConflicts[id] || inTarget && t.Revision == s.Revision:
			next.Chats[id] = s.Revision
		default: // changed on the target only, or deleted there
			if b, ok := base.Chats[id]; ok {
				next.Chats[id] = b
			}
		}
	}
	return next
}

// header is the first entry of a copy's archive.
type header struct {
	From          string            `json:"from"` // the source device, for conflict names
	Conflicts     []string          `json:"conflicts"`
	Delete        []string          `json:"delete"`
	ChatConflicts []string          `json:"chatConflicts"`
	SourceHashes  map[string]string `json:"sourceHashes,omitempty"`
	TargetHashes  map[string]string `json:"targetHashes,omitempty"`
}

// Export writes the plan's files and chats as an archive.
func Export(w io.Writer, root, from string, plan Plan, chats []Chat) error {
	archive := tar.NewWriter(w)
	head, _ := json.Marshal(header{From: from, Conflicts: plan.Conflicts, Delete: plan.Delete, ChatConflicts: plan.ChatConflicts, SourceHashes: plan.SourceHashes, TargetHashes: plan.TargetHashes})
	if err := writeEntry(archive, "sync.json", 0600, head); err != nil {
		return err
	}
	for _, p := range append(append([]string{}, plan.Copy...), plan.Conflicts...) {
		full, err := inside(root, p)
		if err != nil || excluded(p) {
			return fmt.Errorf("invalid file in copy: %s", p)
		}
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxFile {
			return fmt.Errorf("unsupported file in copy: %s", p)
		}
		file, err := os.Open(full)
		if err != nil {
			return err
		}
		err = archive.WriteHeader(&tar.Header{Name: "files/" + p, Mode: int64(info.Mode().Perm()), Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg})
		if err == nil {
			_, err = io.CopyN(archive, file, info.Size())
		}
		file.Close()
		if err != nil {
			return err
		}
	}
	wanted := map[string]bool{}
	for _, id := range append(append([]string{}, plan.Chats...), plan.ChatConflicts...) {
		wanted[id] = true
	}
	for _, chat := range chats {
		if !wanted[chat.ID] {
			continue
		}
		data, err := json.Marshal(chat.Portable())
		if err != nil {
			return err
		}
		if err := writeEntry(archive, "chats/"+chat.ID+".json", 0600, data); err != nil {
			return err
		}
	}
	return archive.Close()
}

func writeEntry(archive *tar.Writer, name string, mode int64, data []byte) error {
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := archive.Write(data)
	return err
}

// Result is what an import did.
type Result struct {
	Files     int      `json:"files"`
	Deleted   int      `json:"deleted"`
	Chats     int      `json:"chats"`
	Conflicts []string `json:"conflicts"` // files and chats kept twice, as named on the target
}

// Import applies an archive to root: files written whole (a temporary file
// renamed), conflicting ones kept under another name, deleted ones moved
// to trash. addChat stores a chat; conflict asks for a copy beside the
// target's own.
func Import(r io.Reader, root, trash string, addChat func(chat Chat, conflict bool) (string, error)) (Result, error) {
	var result Result
	archive := tar.NewReader(r)
	var head header
	seenHeader := false
	conflicts := map[string]bool{}
	stamp := time.Now().Format("2006-01-02 15.04.05") + "-" + core.Identifier()[:8]
	for {
		entry, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		if entry.Typeflag != tar.TypeReg || entry.Size < 0 || entry.Size > maxFile {
			return result, fmt.Errorf("unsupported entry in project copy")
		}
		if !seenHeader && entry.Name != "sync.json" {
			return result, fmt.Errorf("missing copy manifest")
		}
		switch {
		case entry.Name == "sync.json":
			if seenHeader {
				return result, fmt.Errorf("duplicate copy manifest")
			}
			seenHeader = true
			if err := json.NewDecoder(io.LimitReader(archive, 16<<20)).Decode(&head); err != nil {
				return result, err
			}
			for _, p := range head.Conflicts {
				conflicts[p] = true
			}
			for _, p := range head.ChatConflicts {
				conflicts["chat:"+p] = true
			}
			for _, p := range head.Delete {
				if excluded(p) {
					return result, fmt.Errorf("private/generated path in copy: %s", p)
				}
				if err := checkTarget(root, p, head.TargetHashes); err != nil {
					return result, err
				}
				if err := moveToTrash(root, trash, p, stamp); err != nil && !errors.Is(err, os.ErrNotExist) {
					return result, err
				}
				result.Deleted++
			}
		case strings.HasPrefix(entry.Name, "files/"):
			relative := strings.TrimPrefix(entry.Name, "files/")
			if excluded(relative) {
				return result, fmt.Errorf("private/generated path in copy: %s", relative)
			}
			target, err := inside(root, relative)
			if err != nil {
				return result, err
			}
			beforeInstall := func() error {
				if err := checkTarget(root, relative, head.TargetHashes); err != nil {
					return err
				}
				if conflicts[relative] {
					if _, err := os.Lstat(target); err == nil {
						aside := conflictName(target, head.From, stamp)
						if err := os.Rename(target, aside); err != nil {
							return err
						}
						rel, _ := filepath.Rel(root, aside)
						result.Conflicts = append(result.Conflicts, filepath.ToSlash(rel))
					}
				}
				return nil
			}
			if err := writeVerifiedFile(target, archive, os.FileMode(entry.Mode).Perm()|0600, head.SourceHashes[relative], beforeInstall); err != nil {
				return result, fmt.Errorf("%s: %w", relative, err)
			}
			result.Files++
		case strings.HasPrefix(entry.Name, "chats/") && strings.HasSuffix(entry.Name, ".json"):
			var chat Chat
			if err := json.NewDecoder(io.LimitReader(archive, 512<<20)).Decode(&chat); err != nil {
				return result, err
			}
			if chat.ID == "" || strings.ContainsAny(chat.ID, `/\\`) || filepath.Base(entry.Name) != chat.ID+".json" {
				return result, fmt.Errorf("invalid conversation identity")
			}
			chat = chat.Portable()
			title, err := addChat(chat, conflicts["chat:"+chat.ID])
			if err != nil {
				return result, err
			}
			if conflicts["chat:"+chat.ID] {
				result.Conflicts = append(result.Conflicts, "chat “"+title+"”")
			}
			result.Chats++
		}
	}
	if !seenHeader {
		return result, fmt.Errorf("missing copy manifest")
	}
	return result, nil
}

// inside resolves an archive path in root, refusing any way out of it.
func inside(root, relative string) (string, error) {
	if strings.ContainsAny(relative, `\\`) || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", fmt.Errorf("invalid path in the copy: %q", relative)
	}
	clean := path.Clean("/" + relative)[1:]
	if clean == "" || clean != relative && clean != strings.TrimSuffix(relative, "/") {
		return "", fmt.Errorf("invalid path in the copy: %q", relative)
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path in the copy: %q", relative)
	}
	// No link on the way may lead outside the project.
	for dir := filepath.Dir(target); dir != root && strings.HasPrefix(dir, root); dir = filepath.Dir(dir) {
		if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("invalid path in the copy: %q", relative)
		}
	}
	return target, nil
}

func writeFile(target string, from io.Reader, mode os.FileMode) error {
	return writeVerifiedFile(target, from, mode, "", nil)
}

func checkTarget(root, relative string, expected map[string]string) error {
	wanted, known := expected[relative]
	if !known {
		return nil
	}
	target, err := inside(root, relative)
	if err != nil {
		return err
	}
	actual, err := fileHash(target)
	if os.IsNotExist(err) && wanted == "" {
		return nil
	}
	if err != nil || actual != wanted {
		return fmt.Errorf("%s changed during the copy: compare again before retrying", relative)
	}
	return nil
}

func writeVerifiedFile(target string, from io.Reader, mode os.FileMode, expected string, beforeInstall func() error) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("a link is in the way")
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".1loop-sync-*")
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(temporary, hash), from)
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(temporary.Name())
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if expected != "" && hex.EncodeToString(hash.Sum(nil)) != expected {
		os.Remove(temporary.Name())
		return fmt.Errorf("the source changed during transfer: retry the copy")
	}
	if beforeInstall != nil {
		if err := beforeInstall(); err != nil {
			os.Remove(temporary.Name())
			return err
		}
	}
	os.Chmod(temporary.Name(), mode)
	if err := os.Rename(temporary.Name(), target); err != nil {
		os.Remove(temporary.Name())
		return err
	}
	return nil
}

// conflictName is "name (from Phone 2026-10-21 14.03).ext" beside the file.
func conflictName(target, from, stamp string) string {
	extension := filepath.Ext(target)
	base := strings.TrimSuffix(target, extension)
	from = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < 32 {
			return '_'
		}
		return r
	}, from)
	if from == "" {
		from = "copy"
	}
	return fmt.Sprintf("%s (before copy from %s %s)%s", base, from, stamp, extension)
}

func moveToTrash(root, trash, relative, stamp string) error {
	source, err := inside(root, relative)
	if err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", relative)
	}
	destination := filepath.Join(trash, stamp, filepath.Base(root), filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		// Another file system: copy, then remove the original.
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		werr := writeFile(destination, in, 0600)
		in.Close()
		if werr != nil {
			return werr
		}
		return os.Remove(source)
	}
	return nil
}

// TrashDir is where a copy puts the files it removes.
func TrashDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "1loop", "sync-trash")
}
