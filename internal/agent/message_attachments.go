// SPDX-License-Identifier: MPL-2.0

package agent

// Files an agent attaches to a message for another chat (the chats tool's
// message, the subchat tool's start): files of the workspace or of /tmp,
// read here and attached to the message as the composer attaches them. A
// picture goes as an image the receiving model sees, as if pasted; any
// other file is copied into the receiving chat's workspace (its tmp/
// folder) and listed with the message, as a file the user attached.

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"  // DecodeConfig of an attached GIF
	_ "image/jpeg" // and JPEG
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	_ "golang.org/x/image/webp" // and WebP

	"1loop/internal/core"
)

// MaxMessageFiles is how many files one message may carry.
const MaxMessageFiles = 10

// maxMessageFile bounds one attached file, maxMessageFiles them all, and
// maxMessageImage a picture as sent: providers refuse a larger one
// (Anthropic's limit is 5 MB).
const (
	maxMessageFile  = 20 << 20
	maxMessageFiles = 50 << 20
	maxMessageImage = 5 << 20
)

// messageImageTypes are the picture formats every vision model takes.
var messageImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// SentFile is a file attached to a message for another chat, read from the
// sender's workspace: the receiving host copies it into its own.
type SentFile struct {
	Name string
	Data []byte
}

// ChatAttachments are the files a message to another chat carries:
// pictures for its model to see, and other files to copy for it.
type ChatAttachments struct {
	Images []core.ImagePart
	Files  []SentFile
}

// filesProperty is the tools' files parameter.
func filesProperty(use string) map[string]any {
	return map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"},
		"description": fmt.Sprintf("%s: optional files to attach, as paths in the workspace or /tmp (at most %d, %d MiB each). PNG, JPEG, WebP and GIF pictures arrive as images its model sees, like pasted ones; other files are copied into its workspace and attached, like files the user attaches.", use, MaxMessageFiles, maxMessageFile>>20)}
}

// messageAttachments reads the files named by paths. A file that cannot be
// attached refuses the whole message, with why, so the agent can fix it.
func messageAttachments(ctx context.Context, workspace string, paths []string) (ChatAttachments, error) {
	var names []string
	for _, name := range paths {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) > MaxMessageFiles {
		return ChatAttachments{}, fmt.Errorf("Attach at most %d files to a message", MaxMessageFiles)
	}
	var out ChatAttachments
	total := 0
	for _, name := range names {
		data, err := readMessageFile(ctx, workspace, name)
		if err != nil {
			return ChatAttachments{}, fmt.Errorf("Cannot attach %s: %v", name, err)
		}
		if total += len(data); total > maxMessageFiles {
			return ChatAttachments{}, fmt.Errorf("Cannot attach %s: the files together are larger than %d MiB", name, maxMessageFiles>>20)
		}
		if part, ok, err := messageImage(data); err != nil {
			return ChatAttachments{}, fmt.Errorf("Cannot attach %s: %v", name, err)
		} else if ok {
			out.Images = append(out.Images, part)
			continue
		}
		out.Files = append(out.Files, SentFile{Name: path.Base(filepath.ToSlash(name)), Data: data})
	}
	return out, nil
}

// messageImage is data as a picture to send, ok false when it is not one
// (it then goes as a file).
func messageImage(data []byte) (part core.ImagePart, ok bool, err error) {
	mime := http.DetectContentType(data)
	if !messageImageTypes[mime] || !core.ImageDecodable(data) {
		return core.ImagePart{}, false, nil
	}
	part = core.ImagePart{Mime: mime, Data: base64.StdEncoding.EncodeToString(data)}
	// A big picture is scaled down as tool screenshots are: models see no
	// more detail, and a provider refuses one above maxMessageImage.
	if len(data) > maxMessageImage || imageSide(data) > toolImageSide {
		part = compactToolImage(part)
	}
	if base64.StdEncoding.DecodedLen(len(part.Data)) > maxMessageImage {
		return core.ImagePart{}, false, fmt.Errorf("the picture is too large to send even scaled down (at most %d MiB)", maxMessageImage>>20)
	}
	return part, true, nil
}

// imageSide is the longest side of the picture in data.
func imageSide(data []byte) int {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0
	}
	return max(config.Width, config.Height)
}

// readMessageFile reads name, a path in workspace or in /tmp, here or on
// the run's machine.
func readMessageFile(ctx context.Context, workspace, name string) ([]byte, error) {
	check := func(info os.FileInfo) error {
		switch {
		case info.IsDir():
			return fmt.Errorf("it is a folder: attach files")
		case !info.Mode().IsRegular():
			return fmt.Errorf("not a regular file")
		case info.Size() > maxMessageFile:
			return fmt.Errorf("larger than %d MiB", maxMessageFile>>20)
		}
		return nil
	}
	if machine := machineOf(ctx); machine != nil {
		target := path.Clean(name)
		if !strings.HasPrefix(target, "/tmp/") {
			var err error
			if target, err = MachinePath(ctx, machine, workspace, name); err != nil {
				return nil, err
			}
		}
		info, err := machine.Lstat(ctx, target)
		if err != nil {
			return nil, err
		}
		if err := check(info); err != nil {
			return nil, err
		}
		return machine.ReadFile(ctx, target)
	}
	found, err := WorkspacePath(workspace, name)
	if err != nil && filepath.IsAbs(name) {
		for _, root := range tempRoots() {
			if inTemp, problem := WorkspacePath(root, name); problem == nil {
				found, err = inTemp, nil
				break
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("give a file in the workspace or in /tmp")
	}
	// Before opening: opening a named pipe waits for a writer, forever.
	info, err := os.Stat(found)
	if err != nil {
		return nil, err
	}
	if err := check(info); err != nil {
		return nil, err
	}
	file, err := os.Open(found)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxMessageFile+1))
	if err == nil && len(data) > maxMessageFile {
		err = fmt.Errorf("larger than %d MiB", maxMessageFile>>20)
	}
	return data, err
}

// tempRoots are the folders besides the workspace a file may come from,
// with their links resolved, as WorkspacePath resolves the path's.
func tempRoots() []string {
	var roots []string
	for _, root := range []string{"/tmp", os.TempDir()} {
		if resolved, err := filepath.EvalSymlinks(root); err == nil && !slices.Contains(roots, resolved) {
			roots = append(roots, resolved)
		}
	}
	return roots
}

// DeliverFiles copies the files sent to a chat into its workspace, on its
// SSH machine when machine is set, as the composer copies attached files.
func DeliverFiles(ctx context.Context, machine Machine, workspace string, files []SentFile) ([]FileAttachment, error) {
	var out []FileAttachment
	for _, file := range files {
		var attached FileAttachment
		var err error
		if machine != nil {
			attached, err = CopyAttachmentTo(ctx, machine, workspace, file.Name, bytes.NewReader(file.Data))
		} else {
			attached, err = CopyAttachment(workspace, file.Name, bytes.NewReader(file.Data))
		}
		if err != nil {
			return nil, fmt.Errorf("Could not copy %s into the other chat's workspace: %v", file.Name, err)
		}
		out = append(out, attached)
	}
	return out, nil
}

// AttachmentsNote is what the tool result adds about the files sent to a
// chat on provider: whether its model sees the pictures.
func AttachmentsNote(provider string, attachments ChatAttachments) string {
	var note string
	if count := len(attachments.Files); count > 0 {
		note += fmt.Sprintf(" %s went into its workspace's tmp/ folder, listed with the message.", plural(count, "file"))
	}
	count := len(attachments.Images)
	if count == 0 {
		return note
	}
	images := plural(count, "image")
	switch provider {
	case "antigravity":
		return note + " " + images + " reached it as files its Antigravity agent opens with its file viewer."
	case "vllm", "compatible", "openrouter":
		return note + " " + images + " went with the message; its model sees them only if it takes images (vision): one that does not will not see them, so describe what matters in the text."
	case "":
		return note + " " + images + " went with the message."
	}
	return note + " " + images + " went with the message, and its model sees them."
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// filesPreview is the attached files' line in an approval's preview.
func filesPreview(paths []string) string {
	var names []string
	for _, name := range paths {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "\n\nAttached: " + strings.Join(names, ", ")
}
