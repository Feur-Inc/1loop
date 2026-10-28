package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/agent"
)

type fileAttachment = agent.FileAttachment

// copyAttachment copies a file of this computer into the workspace: here,
// or on the SSH machine when machine is set.
func copyAttachment(machine agent.Machine, workspace, source string) (fileAttachment, error) {
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
	if machine != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return agent.CopyAttachmentTo(ctx, machine, workspace, filepath.Base(source), in)
	}
	return agent.CopyAttachment(workspace, filepath.Base(source), in)
}

func (ui *desktopUI) pickFiles() {
	ui.pick(pickRequest{title: "Attach files", start: ui.pickStart(), multiple: true}, func(paths []string) {
		ui.attachFiles(paths)
	})
}

func (ui *desktopUI) attachFiles(paths []string) {
	workspace := ui.cfg.Workspace
	var machine agent.Machine
	if found := ui.machineFor(ui.cfg.MachineID); found != nil {
		machine = found
	}
	ui.filesAttaching++
	go func() {
		var files []fileAttachment
		var problems []string
		for _, source := range paths {
			file, err := copyAttachment(machine, workspace, source)
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
