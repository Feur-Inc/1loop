package ui

// The Files tab of the remote desktop: the server project's files, through
// its /api/files routes. Download saves into the computer's Downloads.

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"

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
	if view, ok := remote.tabViews["files"]; ok {
		for _, item := range remote.tabs.Items {
			if item.Content == view {
				remote.tabs.Select(item)
				return
			}
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
		}},
		{label: "Download", icon: iconDocument, run: func(file remoteapi.FileEntry) { remote.download(query(file.Path), file.Name) }},
		{label: "Copy path", icon: iconCopy, run: func(file remoteapi.FileEntry) { fyne.CurrentApp().Clipboard().SetContent(file.Path) }},
	}
	pick := func(done func([]string)) {
		home, _ := os.UserHomeDir()
		remote.parent.pick(pickRequest{title: "Add files to the server project", start: home, multiple: true}, done)
	}
	view := newFileBrowser(ctx, remote.window, source, actions, pick)
	remote.tabViews["files"] = view
	remote.tabCancel["files"] = cancel
	item := container.NewTabItem("Files", view)
	remote.tabs.Append(item)
	remote.tabs.Select(item)
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
	var address serverAddress
	data, err := os.ReadFile(remoteAddressPath())
	if err == nil {
		err = json.Unmarshal(data, &address)
	}
	if err != nil || address.Token == "" {
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
