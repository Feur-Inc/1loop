// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"fmt"
	"io"
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
	// Before opening: opening a named pipe waits for a writer, forever.
	if info, err := os.Stat(source); err != nil {
		return fileAttachment{}, err
	} else if !info.Mode().IsRegular() {
		return fileAttachment{}, fmt.Errorf("Choose a regular file")
	}
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
	return writeAttachment(machine, workspace, filepath.Base(source), in)
}

// writeAttachment saves an attachment's content in the workspace: here, or
// on the SSH machine when machine is set.
func writeAttachment(machine agent.Machine, workspace, name string, content io.Reader) (fileAttachment, error) {
	if machine != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		return agent.CopyAttachmentTo(ctx, machine, workspace, name, content)
	}
	return agent.CopyAttachment(workspace, name, content)
}

func (ui *desktopUI) pickFiles() {
	if ui.server != nil {
		ui.server.pickAttachments(false)
		return
	}
	ui.pick(pickRequest{title: "Attach files", start: ui.pickStart(), multiple: true}, func(paths []string) {
		ui.attachFiles(paths)
	})
}

func (ui *desktopUI) attachFiles(paths []string) {
	if ui.server != nil { // a server chat: its files go to the server
		ui.server.attachPaths(paths)
		return
	}
	ui.copyAttachments(paths, copyAttachment)
}

// attachText attaches a text too long for the prompt as a file (#238).
func (ui *desktopUI) attachText(text string) {
	if ui.server != nil {
		ui.server.attachBytes("pasted-text.txt", []byte(text))
		return
	}
	ui.copyAttachments([]string{"pasted-text.txt"}, func(machine agent.Machine, workspace, name string) (fileAttachment, error) {
		return writeAttachment(machine, workspace, name, strings.NewReader(text))
	})
}

// copyAttachments makes an attachment of each source, off the interface
// thread, and stages them for the next message.
func (ui *desktopUI) copyAttachments(sources []string, write func(machine agent.Machine, workspace, source string) (fileAttachment, error)) {
	workspace := ui.cfg.Workspace
	var machine agent.Machine
	if found := ui.machineFor(ui.cfg.MachineID); found != nil {
		machine = found
	}
	ui.filesAttaching++
	go func() {
		var files []fileAttachment
		var problems []string
		for _, source := range sources {
			file, err := write(machine, workspace, source)
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
