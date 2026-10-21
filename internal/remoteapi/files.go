package remoteapi

// A project's files for the file browser (desktop, remote desktop, phone):
// list a folder, read a file, download it, add one. Every path stays inside
// the project; the routes are behind pairing like the others.

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// FileLimit is the largest file the browser adds to a project.
const FileLimit = 512 << 20

// previewLimit is how much of a text file a preview shows.
const previewLimit = 1 << 20

var uploadNameMu sync.Mutex

type FileEntry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"` // relative to the project
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// projectPath resolves a path the client sent, relative to the project.
func projectPath(root, relative string) (string, error) {
	relative = filepath.FromSlash(strings.TrimPrefix(relative, "/"))
	if relative == "" || relative == "." {
		return root, nil
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("The path is outside the project")
	}
	return agent.WorkspacePath(root, relative)
}

func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ListFiles is GET /api/files?workspace=&path=: a folder's entries, folders
// first. Git's own folder is left out, as editors do.
func ListFiles(w http.ResponseWriter, r *http.Request, root string) {
	path, entries, err := ListDir(root, r.URL.Query().Get("path"))
	if err != nil {
		core.Fail(w, http.StatusNotFound, err.Error())
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]any{"path": path, "entries": entries})
}

// ListDir lists a folder of the project, relative to it.
func ListDir(root, relative string) (string, []FileEntry, error) {
	dir, err := projectPath(root, relative)
	if err != nil {
		return "", nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Errorf("This folder cannot be read")
	}
	out := []FileEntry{}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		dirEntry := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 { // a link is listed as what it points to, inside the project only
			target, err := agent.WorkspacePath(root, filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			if stat, err := os.Stat(target); err == nil {
				dirEntry, info = stat.IsDir(), stat
			}
		}
		out = append(out, FileEntry{Name: entry.Name(), Path: relativePath(root, filepath.Join(dir, entry.Name())), Dir: dirEntry, Size: info.Size(), Modified: info.ModTime()})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return relativePath(root, dir), out, nil
}

// FilePreview is a file as the browser shows it.
type FilePreview struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Text      string `json:"text,omitempty"`
	Binary    bool   `json:"binary"`
	Image     bool   `json:"image"`
	Truncated bool   `json:"truncated"`
}

// ReadPreview reads a file for its preview: text (up to previewLimit), or
// whether it is an image or another binary file.
func ReadPreview(root, relative string) (FilePreview, error) {
	path, err := projectPath(root, relative)
	if err != nil {
		return FilePreview{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return FilePreview{}, fmt.Errorf("This file cannot be read")
	}
	file, err := os.Open(path)
	if err != nil {
		return FilePreview{}, fmt.Errorf("This file cannot be read")
	}
	defer file.Close()
	data, _ := io.ReadAll(io.LimitReader(file, previewLimit))
	preview := FilePreview{Path: relativePath(root, path), Size: info.Size(), Truncated: info.Size() > previewLimit}
	kind := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(kind, "image/"):
		preview.Image = true
	case bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(trimRune(data)):
		preview.Binary = true
	default:
		preview.Text = string(trimRune(data))
	}
	return preview, nil
}

// trimRune drops a multi-byte character the limit cut in half.
func trimRune(data []byte) []byte {
	for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
		data = data[:len(data)-1]
	}
	return data
}

// FileContent is GET /api/files/content?workspace=&path=.
func FileContent(w http.ResponseWriter, r *http.Request, root string) {
	preview, err := ReadPreview(root, r.URL.Query().Get("path"))
	if err != nil {
		core.Fail(w, http.StatusNotFound, err.Error())
		return
	}
	core.WriteJSON(w, http.StatusOK, preview)
}

// DownloadFile is GET /api/files/download?workspace=&path=: the file itself.
func DownloadFile(w http.ResponseWriter, r *http.Request, root string) {
	path, err := projectPath(root, r.URL.Query().Get("path"))
	if err != nil {
		core.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		core.Fail(w, http.StatusNotFound, "Choose a regular file")
		return
	}
	file, err := os.Open(path)
	if err != nil {
		core.Fail(w, http.StatusNotFound, "This file cannot be read")
		return
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		core.Fail(w, http.StatusNotFound, "This file cannot be read")
		return
	}
	kind := mime.TypeByExtension(filepath.Ext(path))
	if kind == "" {
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	io.Copy(w, file)
}

// UploadFile is POST /api/files/upload?workspace=&path=<folder>&name=: the
// body becomes a file of the project; an existing name gets a number.
func UploadFile(w http.ResponseWriter, r *http.Request, root string) {
	r.Body = http.MaxBytesReader(w, r.Body, FileLimit)
	path, err := SaveFile(root, r.URL.Query().Get("path"), r.URL.Query().Get("name"), r.Body)
	if err != nil {
		core.Fail(w, http.StatusBadRequest, err.Error())
		return
	}
	core.WriteJSON(w, http.StatusOK, map[string]string{"path": path})
}

// SaveFile writes a new file in a folder of the project, whole or not at
// all, and returns its path relative to the project.
func SaveFile(root, folder, name string, from io.Reader) (string, error) {
	dir, err := projectPath(root, folder)
	if err != nil {
		return "", err
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("Invalid file name")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("Choose a folder of the project")
	}
	temporary, err := os.CreateTemp(dir, ".1loop-upload-*")
	if err != nil {
		return "", fmt.Errorf("Cannot write in this folder")
	}
	copied, copyErr := io.Copy(temporary, io.LimitReader(from, FileLimit+1))
	closeErr := temporary.Close()
	if copyErr != nil || closeErr != nil || copied > FileLimit {
		os.Remove(temporary.Name())
		return "", fmt.Errorf("The file did not arrive whole")
	}
	// Choose the name only after receiving the file; simultaneous uploads
	// cannot select the same destination while their bodies are streaming.
	uploadNameMu.Lock()
	defer uploadNameMu.Unlock()
	path, err := freeName(root, dir, name)
	if err != nil {
		os.Remove(temporary.Name())
		return "", err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		os.Remove(temporary.Name())
		return "", fmt.Errorf("Cannot write in this folder")
	}
	return relativePath(root, path), nil
}

// freeName is name in dir, or "name (2).ext" and so on when it is taken.
func freeName(root, dir, name string) (string, error) {
	extension := filepath.Ext(name)
	base := strings.TrimSuffix(name, extension)
	for i := 1; i < 1000; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, extension)
		}
		path, err := agent.WorkspacePath(root, filepath.Join(dir, candidate))
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return path, nil
		}
	}
	return "", fmt.Errorf("Too many files with this name")
}
