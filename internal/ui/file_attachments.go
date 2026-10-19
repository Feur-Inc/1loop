package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
)

type fileAttachment = agent.FileAttachment

func copyAttachment(workspace, source string) (fileAttachment, error) {
	in, err := os.Open(source)
	if err != nil {
		return fileAttachment{}, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fileAttachment{}, err
	}
	if !info.Mode().IsRegular() {
		return fileAttachment{}, fmt.Errorf("Choose a regular file")
	}
	return agent.CopyAttachment(workspace, filepath.Base(source), in)
}

func (ui *desktopUI) pickFiles() {
	ui.pick(pickRequest{title: "Attach files", start: ui.cfg.Workspace, multiple: true}, func(paths []string) {
		workspace := ui.cfg.Workspace
		ui.filesAttaching++
		go func() {
			var files []fileAttachment
			var problems []string
			for _, source := range paths {
				file, err := copyAttachment(workspace, source)
				if err != nil {
					problems = append(problems, filepath.Base(source)+": "+err.Error())
					continue
				}
				files = append(files, file)
			}
			fyne.Do(func() {
				ui.filesAttaching--
				ui.fileAttachments = append(ui.fileAttachments, files...)
				ui.renderAttachments()
				if len(problems) > 0 {
					ui.notice("Attach files", strings.Join(problems, "\n"))
				}
			})
		}()
	})
}

func attachmentText(text string, files []fileAttachment) string {
	return agent.AttachmentText(text, files)
}

func (ui *desktopUI) attachmentProblem() string {
	if ui.filesAttaching > 0 {
		return "Files are still being copied. Wait before sending."
	}
	for _, file := range ui.fileAttachments {
		if filepath.Clean(file.Workspace) != filepath.Clean(ui.cfg.Workspace) {
			return "Attached files belong to another workspace. Remove them or return to that workspace before sending."
		}
	}
	return ""
}
