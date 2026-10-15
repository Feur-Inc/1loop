package ui

// The side panel's terminal and changes tabs on the desktop.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fyneterm "github.com/fyne-io/terminal"

	"1loop/internal/agent"
	"1loop/internal/core"
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
