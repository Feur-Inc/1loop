// SPDX-License-Identifier: MPL-2.0

package ui

// The Files tab of a server chat: the server project's files, through its
// /api/files routes. Download saves into the computer's Downloads.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/remoteapi"
)

func (remote *remoteDesktop) workspace() string {
	for _, chat := range remote.chats {
		if chat.ID == remote.current {
			return chat.Workspace
		}
	}
	return remote.defaultWorkspace
}

func (remote *remoteDesktop) openFiles() {
	panel := remote.parent.panel
	for i, tab := range panel.tabs {
		if tab.kind == "server:files" {
			panel.choose(i)
			panel.unfold()
			return
		}
	}
	ctx, cancel := context.WithCancel(remote.ctx)
	workspace := remote.workspace()
	query := func(file string) string {
		return "workspace=" + url.QueryEscape(workspace) + "&path=" + url.QueryEscape(file)
	}
	name := path.Base(filepath.ToSlash(workspace))
	if workspace == "" {
		name = "Project"
	}
	source := fileSource{
		name: name,
		list: func(ctx context.Context, folder string) ([]remoteapi.FileEntry, error) {
			var reply struct{ Entries []remoteapi.FileEntry }
			err := remote.client.json(ctx, "GET", "/api/files?"+query(folder), nil, &reply)
			return reply.Entries, err
		},
		preview: func(ctx context.Context, file string) (remoteapi.FilePreview, error) {
			var preview remoteapi.FilePreview
			err := remote.client.json(ctx, "GET", "/api/files/content?"+query(file), nil, &preview)
			return preview, err
		},
		picture: func(ctx context.Context, file string) (image.Image, error) {
			response, err := remote.client.request(ctx, "GET", "/api/files/download?"+query(file), nil)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			picture, _, err := image.Decode(io.LimitReader(response.Body, 64<<20))
			return picture, err
		},
		add: func(ctx context.Context, folder string, paths []string) error {
			for _, local := range paths {
				in, err := os.Open(local)
				if err != nil {
					return err
				}
				err = remote.client.json(ctx, "POST", "/api/files/upload?"+query(folder)+"&name="+url.QueryEscape(filepath.Base(local)), in, nil)
				in.Close()
				if err != nil {
					return fmt.Errorf("%s: %w", filepath.Base(local), err)
				}
			}
			return nil
		},
	}
	actions := []fileAction{
		{label: "Attach", icon: iconPlus, run: func(file remoteapi.FileEntry) {
			remote.fileAttachments = append(remote.fileAttachments, agent.FileAttachment{Name: file.Name, Path: file.Path, Workspace: workspace})
			remote.status.SetText(fmt.Sprintf("%d file(s) attached to the next message", len(remote.fileAttachments)))
			remote.parent.renderAttachments()
		}},
		{label: "Download", icon: iconDocument, run: func(file remoteapi.FileEntry) { remote.download(query(file.Path), file.Name) }},
		{label: "Copy path", icon: iconCopy, run: func(file remoteapi.FileEntry) { fyne.CurrentApp().Clipboard().SetContent(file.Path) }},
	}
	pick := func(done func([]string)) {
		home, _ := os.UserHomeDir()
		remote.parent.pick(pickRequest{title: "Add files to the server project", start: home, multiple: true}, done)
	}
	view := newFileBrowser(ctx, remote.window, source, actions, pick)
	remote.addPanelTab("files", "Files", "files", view, cancel, func(bool) {}, true)
}

// download saves a server file into ~/Downloads, never over another file.
func (remote *remoteDesktop) download(query, name string) {
	go func() {
		ctx, cancel := context.WithCancel(remote.ctx)
		defer cancel()
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, "Downloads")
		err := os.MkdirAll(dir, 0755)
		var saved string
		if err == nil {
			var response *http.Response
			response, err = remote.client.request(ctx, "GET", "/api/files/download?"+query, nil)
			if err == nil {
				defer response.Body.Close()
				saved, err = remoteapi.SaveFile(dir, "", name, response.Body)
				saved = filepath.Join(dir, saved)
			}
		}
		fyne.Do(func() {
			if err != nil {
				remote.status.SetText("Download: " + err.Error())
			} else {
				remote.status.SetText("Downloaded to " + saved)
			}
		})
	}()
}

// sendProjectFile uses the last paired engine; it never creates a new trust relationship.
func (ui *desktopUI) sendProjectFile(root string, file remoteapi.FileEntry) {
	address := loadServerAddress()
	if address.Token == "" {
		ui.notice("Send file", "Connect and pair the destination first using Remote server.")
		return
	}
	client, err := newRemoteClient(address)
	if err != nil {
		ui.notice("Send file", err.Error())
		return
	}
	folder := widget.NewEntry()
	folder.SetPlaceHolder("Leave empty for its current project")
	status := note("The file is copied to the selected project on " + address.URL + ". Existing names get a number.")
	var sheet *sheet
	send := newRow("Send", nil, stylePrimary, nil)
	send.onTap = func() {
		workspace := folder.Text
		send.Disable()
		go func() {
			input, err := os.Open(filepath.Join(root, filepath.FromSlash(file.Path)))
			if err == nil {
				defer input.Close()
				err = client.json(ui.ctx, "POST", "/api/files/upload?workspace="+url.QueryEscape(workspace)+"&name="+url.QueryEscape(file.Name), input, nil)
			}
			fyne.Do(func() {
				send.Enable()
				if err != nil {
					status.SetText(err.Error())
				} else {
					sheet.dismiss()
				}
			})
		}()
	}
	sheet = ui.modal("Send “"+file.Name+"”", container.NewVBox(status, widget.NewLabel("Destination project folder"), folder), 620, nil, send)
}

// remoteUpload is a file for a server chat's composer: one of this
// computer's, or the browser's, whose bytes the server copies.
type remoteUpload struct {
	name string
	open func() (io.ReadCloser, error) // called off the interface thread
}

// attachPaths uploads this computer's files as the next message's.
func (remote *remoteDesktop) attachPaths(paths []string) {
	var uploads []remoteUpload
	for _, local := range paths {
		uploads = append(uploads, remoteUpload{name: filepath.Base(local), open: func() (io.ReadCloser, error) {
			if info, err := os.Stat(local); err != nil {
				return nil, err
			} else if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("Choose a regular file")
			}
			return os.Open(local)
		}})
	}
	remote.attach(uploads)
}

// attachBytes uploads content made here (a pasted picture or text).
func (remote *remoteDesktop) attachBytes(name string, data []byte) {
	remote.attach([]remoteUpload{{name: name, open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }}})
}

// attach copies files into the project of the chat on screen, on the
// server, where the agent reads them: the composer shows them until the
// message goes. The server sends the pictures among them as images.
func (remote *remoteDesktop) attach(uploads []remoteUpload) {
	ui := remote.parent
	chat, workspace := remote.current, remote.workspace()
	if chat == "" && ui.cfg.Workspace != "" {
		workspace = ui.cfg.Workspace // where post sends a new chat
	}
	ui.filesAttaching++
	go func() {
		var files []agent.FileAttachment
		var problems []string
		for _, upload := range uploads {
			file, err := remote.upload(workspace, upload)
			if err != nil {
				problems = append(problems, upload.name+": "+err.Error())
				continue
			}
			files = append(files, file)
		}
		fyne.Do(func() {
			ui.filesAttaching--
			if remote.ctx.Err() != nil {
				return
			}
			if remote.current == chat {
				remote.fileAttachments = append(remote.fileAttachments, files...)
				ui.renderAttachments()
			}
			if len(problems) > 0 {
				ui.notice("Attach files", strings.Join(problems, "\n"))
			}
		})
	}()
}

func (remote *remoteDesktop) upload(workspace string, upload remoteUpload) (agent.FileAttachment, error) {
	var file agent.FileAttachment
	content, err := upload.open()
	if err != nil {
		return file, err
	}
	defer content.Close()
	ctx, cancel := context.WithTimeout(remote.ctx, 5*time.Minute)
	defer cancel()
	err = remote.client.json(ctx, "POST", "/api/attachments?workspace="+url.QueryEscape(workspace)+"&name="+url.QueryEscape(upload.name), content, &file)
	return file, err
}
