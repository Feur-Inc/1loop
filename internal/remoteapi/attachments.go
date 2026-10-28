package remoteapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"1loop/internal/agent"
	"1loop/internal/core"
)

const AttachmentLimit = 64 << 20

func UploadAttachment(w http.ResponseWriter, r *http.Request, workspace string) {
	r.Body = http.MaxBytesReader(w, r.Body, AttachmentLimit)
	file, err := agent.CopyAttachment(workspace, r.URL.Query().Get("name"), r.Body)
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, file)
}

// Attachments are files of the project: copied ones (in its tmp folder) or
// ones the file browser attaches. A path the client sent never leaves the
// project, where the agent can already read.
func AttachedMessage(workspace, text string, files []agent.FileAttachment) (agent.Message, error) {
	message := agent.Message{Role: "user", Content: text}
	if len(files) > 20 {
		return message, fmt.Errorf("Attach at most 20 files per message")
	}
	validated := make([]agent.FileAttachment, 0, len(files))
	imageBytes := 0
	for _, file := range files {
		if filepath.Clean(file.Workspace) != filepath.Clean(workspace) {
			return message, fmt.Errorf("Attached files belong to another workspace")
		}
		path, err := agent.WorkspacePath(workspace, file.Path)
		if err != nil {
			return message, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return message, err
		}
		if !info.Mode().IsRegular() {
			return message, fmt.Errorf("Choose a regular file")
		}
		file.Path, file.Name = path, filepath.Base(file.Name)
		if strings.ContainsAny(file.Name, "\r\n\x00") {
			return message, fmt.Errorf("Invalid filename")
		}
		validated = append(validated, file)
		// Large images remain readable as files, rather than exhausting mobile
		// memory or the model request while building their base64 representation.
		if info.Size() <= 8<<20 && imageBytes+int(info.Size()) <= 16<<20 {
			data, err := os.ReadFile(path)
			if err != nil {
				return message, err
			}
			format, _, err := image.DecodeConfig(bytes.NewReader(data))
			mime := http.DetectContentType(data)
			if err == nil && format.Width*format.Height <= 20_000_000 && (mime == "image/png" || mime == "image/jpeg" || mime == "image/gif") {
				imageBytes += len(data)
				message.Images = append(message.Images, core.ImagePart{Mime: mime, Data: base64.StdEncoding.EncodeToString(data)})
				validated[len(validated)-1].Image = true
			}
		}
	}
	return agent.PromptMessage(text, message.Images, validated), nil
}

// FileNames are the names a transcript shows for a prompt's attached files:
// those sent as pictures already appear as images.
func FileNames(files []agent.FileAttachment) []string {
	var names []string
	for _, file := range files {
		if !file.Image {
			names = append(names, file.Name)
		}
	}
	return names
}
