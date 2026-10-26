package ui

// The side panel's terminal, changes and files tabs on the desktop.

import (
	"context"
	"fmt"
	"image"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fyneterm "github.com/fyne-io/terminal"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
	"1loop/internal/terminal"
)

type panelTerminal struct {
	widget *fyneterm.Terminal
	file   terminal.Process
	cancel context.CancelFunc
	id     string
	dir    string
	mu     sync.Mutex
	output []byte
	total  int64
	exit   *int
	done   chan struct{}
	once   sync.Once
}

func (terminal *panelTerminal) close() {
	terminal.once.Do(func() { _ = terminal.file.Close(); terminal.cancel() })
}

func (panel *toolPanel) openTerminal() {
	tab, err := panel.terminalTab(panel.ui.cfg.Workspace)
	if err != nil {
		panel.ui.notice("Terminal", err.Error())
		return
	}
	panel.tabs = append(panel.tabs, tab)
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
}

// terminalTab starts a shell in dir and returns its tab.
func (panel *toolPanel) terminalTab(dir string) (panelTab, error) {
	ctx, cancel := context.WithCancel(panel.ui.ctx)
	file, err := terminal.NewTerminalProcess(ctx, dir)
	if err != nil {
		cancel()
		return panelTab{}, err
	}
	w := fyneterm.New()
	w.SetStartDir(dir)
	w.Resize(fyne.NewSize(600, 400))
	panelTerminalValue := &panelTerminal{widget: w, file: file, cancel: cancel, id: core.Identifier()[:16], dir: dir, done: make(chan struct{})}
	status := textLine(dir, 11, colMuted)
	configs := make(chan fyneterm.Config, 4)
	w.AddListener(configs)
	go func() {
		defer w.RemoveListener(configs)
		for {
			select {
			case <-ctx.Done():
				return
			case cfg := <-configs:
				_ = file.Resize(int(cfg.Columns), int(cfg.Rows))
			}
		}
	}()
	go func() { _ = w.RunWithConnection(file, &terminalOutput{file: file, terminal: panelTerminalValue}) }()
	go func() {
		code := file.Wait()
		panelTerminalValue.mu.Lock()
		panelTerminalValue.exit = &code
		panelTerminalValue.mu.Unlock()
		close(panelTerminalValue.done)
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			status.Text = fmt.Sprintf("Terminal exited · code %d", code)
			if code != 0 {
				status.Color = colDanger
			}
			status.Refresh()
		})
	}()
	return panelTab{kind: "terminal", title: "Terminal", icon: toolGlyph("shell"), terminal: panelTerminalValue, workspace: dir,
		content: container.NewBorder(nil, padded(6, 6, 12, 12, status), nil, nil, padded(10, 4, 12, 12, w))}, nil
}

func (panel *toolPanel) openChanges() {
	for i, tab := range panel.tabs {
		if tab.kind == "changes" {
			panel.choose(i)
			panel.unfold()
			return
		}
	}
	panel.tabs = append(panel.tabs, panel.changesTab(panel.ui.cfg.Workspace))
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
}

// changesTab returns the tab of the Git changes of root, already loading.
func (panel *toolPanel) changesTab(root string) panelTab {
	ctx, cancel := context.WithCancel(panel.ui.ctx)
	body := container.NewStack(note("Loading changes…"))
	mode := newPicker(panel.ui.window, "Changes", styleChip, nil)
	mode.Options = []string{"Unstaged", "Staged"}
	mode.SetSelected(mode.Options[0])
	stats := textLine("", 12, colMuted)
	var generation int
	var refresh *tapRow
	load := func() {
		generation++
		current := generation
		staged := mode.Selected == "Staged"
		refresh.Disable()
		go func() {
			diff, err := agent.WorkspaceDiff(ctx, root, staged)
			fyne.Do(func() {
				if ctx.Err() != nil || current != generation {
					return
				}
				refresh.Enable()
				added, removed := diffStat(diff)
				stats.Text = fmt.Sprintf("+%d  −%d", added, removed)
				stats.Refresh()
				switch {
				case err != nil:
					body.Objects = []fyne.CanvasObject{padded(20, 20, 18, 18, note(err.Error()))}
				case strings.TrimSpace(diff) == "":
					body.Objects = []fyne.CanvasObject{padded(20, 20, 18, 18, note("No changes in this view."))}
				default:
					body.Objects = []fyne.CanvasObject{newDiffView(diff)}
				}
				body.Refresh()
			})
		}()
	}
	refresh = newRow("Refresh", nil, styleOutline, load)
	mode.onChanged = func(string) { load() }
	header := padded(10, 10, 12, 12, container.NewBorder(nil, nil, mode.row, refresh, container.NewCenter(stats)))
	load()
	return panelTab{kind: "changes", title: "Changes", icon: toolGlyph("edit_file"), cancel: cancel, workspace: root,
		content: container.NewBorder(header, nil, nil, nil, body)}
}

// openFileAt shows a citation: the file in the Files tab, its lines marked.
func (panel *toolPanel) openFileAt(file string, from, to int) {
	panel.openFiles()
	for _, tab := range panel.tabs {
		if tab.kind == "files" && tab.files != nil {
			tab.files.showLines(file, from, to)
			return
		}
	}
}

func (panel *toolPanel) openFiles() {
	for i, tab := range panel.tabs {
		if tab.kind == "files" {
			panel.choose(i)
			panel.unfold()
			return
		}
	}
	panel.tabs = append(panel.tabs, panel.filesTab(panel.ui.cfg.Workspace))
	panel.choose(len(panel.tabs) - 1)
	panel.unfold()
}

// filesTab browses the project's files on this computer.
func (panel *toolPanel) filesTab(root string) panelTab {
	ui := panel.ui
	ctx, cancel := context.WithCancel(ui.ctx)
	source := fileSource{
		name: filepath.Base(root),
		list: func(_ context.Context, folder string) ([]remoteapi.FileEntry, error) {
			_, entries, err := remoteapi.ListDir(root, folder)
			return entries, err
		},
		preview: func(_ context.Context, file string) (remoteapi.FilePreview, error) {
			return remoteapi.ReadPreview(root, file)
		},
		picture: func(_ context.Context, file string) (image.Image, error) {
			path, err := agent.WorkspacePath(root, file)
			if err != nil {
				return nil, err
			}
			in, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer in.Close()
			picture, _, err := image.Decode(in)
			return picture, err
		},
		add: func(_ context.Context, folder string, paths []string) error {
			for _, source := range paths {
				in, err := os.Open(source)
				if err != nil {
					return err
				}
				_, err = remoteapi.SaveFile(root, folder, filepath.Base(source), in)
				in.Close()
				if err != nil {
					return fmt.Errorf("%s: %w", filepath.Base(source), err)
				}
			}
			return nil
		},
	}
	actions := []fileAction{
		{label: "Download", icon: iconDocument, run: func(file remoteapi.FileEntry) {
			home, _ := os.UserHomeDir()
			ui.pick(pickRequest{title: "Save a copy in this folder", start: filepath.Join(home, "Downloads"), folder: true}, func(paths []string) {
				if len(paths) == 0 {
					return
				}
				go func() {
					input, err := os.Open(filepath.Join(root, filepath.FromSlash(file.Path)))
					if err == nil {
						defer input.Close()
						_, err = remoteapi.SaveFile(paths[0], "", file.Name, input)
					}
					fyne.Do(func() {
						if err != nil {
							ui.notice("Download", err.Error())
						} else {
							ui.notice("Download", "Saved a copy in "+paths[0])
						}
					})
				}()
			})
		}},
		{label: "Send to…", run: func(file remoteapi.FileEntry) { ui.sendProjectFile(root, file) }},
		{label: "Attach", icon: iconPlus, run: func(file remoteapi.FileEntry) {
			ui.fileAttachments = append(ui.fileAttachments, fileAttachment{Name: file.Name, Path: filepath.Join(root, filepath.FromSlash(file.Path)), Workspace: root})
			ui.renderAttachments()
		}},
		{label: "Open", icon: iconDocument, run: func(file remoteapi.FileEntry) {
			if err := fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: filepath.Join(root, filepath.FromSlash(file.Path))}); err != nil {
				ui.notice("Open file", err.Error())
			}
		}},
		{label: "Copy path", icon: iconCopy, run: func(file remoteapi.FileEntry) { fyne.CurrentApp().Clipboard().SetContent(file.Path) }},
	}
	pick := func(done func([]string)) {
		home, _ := os.UserHomeDir()
		ui.pick(pickRequest{title: "Add files to the project", start: home, multiple: true}, done)
	}
	files := buildFileBrowser(ctx, ui.window, source, actions, pick)
	return panelTab{kind: "files", title: "Files", icon: iconFolder, cancel: cancel, workspace: root, content: files.content, files: files}
}
