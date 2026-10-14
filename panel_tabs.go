package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"github.com/creack/pty"
	"github.com/fyne-io/terminal"
)

type panelTerminal struct {
	widget *terminal.Terminal
	file   *os.File
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
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.CommandContext(ctx, shell, "-i")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	file, err := pty.Start(cmd)
	if err != nil {
		cancel()
		return panelTab{}, err
	}
	w := terminal.New()
	w.SetStartDir(cmd.Dir)
	w.Resize(fyne.NewSize(600, 400))
	panelTerminalValue := &panelTerminal{widget: w, file: file, cancel: cancel, id: identifier()[:16], dir: dir, done: make(chan struct{})}
	status := textLine(cmd.Dir, 11, colMuted)
	configs := make(chan terminal.Config, 4)
	w.AddListener(configs)
	go func() {
		defer w.RemoveListener(configs)
		for {
			select {
			case <-ctx.Done():
				return
			case cfg := <-configs:
				_ = pty.Setsize(file, &pty.Winsize{Rows: uint16(cfg.Rows), Cols: uint16(cfg.Columns)})
			}
		}
	}()
	go func() { _ = w.RunWithConnection(file, &terminalOutput{file: file, terminal: panelTerminalValue}) }()
	go func() {
		err := cmd.Wait()
		code := cmd.ProcessState.ExitCode()
		panelTerminalValue.mu.Lock()
		panelTerminalValue.exit = &code
		panelTerminalValue.mu.Unlock()
		close(panelTerminalValue.done)
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			status.Text = fmt.Sprintf("Terminal exited · code %d", cmd.ProcessState.ExitCode())
			if err != nil {
				status.Color = colDanger
			}
			status.Refresh()
		})
	}()
	return panelTab{kind: "terminal", title: "Terminal", icon: toolGlyph("shell"), terminal: panelTerminalValue, workspace: cmd.Dir,
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
			diff, err := workspaceDiff(ctx, root, staged)
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

func workspaceDiff(ctx context.Context, root string, staged bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-C", root, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3"}
	if staged {
		args = append(args, "--cached")
	}
	args = append(args, "--")
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Unable to read Git changes: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if staged {
		return string(out), nil
	}
	names, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return "", fmt.Errorf("Unable to read new Git files: %w", err)
	}
	var result strings.Builder
	result.Write(out)
	for _, name := range strings.Split(strings.TrimSuffix(string(names), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		fmt.Fprintf(&result, "diff --git a/%s b/%s\n", name, name)
		path, err := workspacePath(root, name)
		var text string
		if err == nil {
			text, err = fileText(path)
		}
		if err != nil {
			result.WriteString("New file · preview unavailable\n")
			continue
		}
		patch, err := unifiedDiff(ctx, name, "", text)
		if err != nil {
			return "", err
		}
		result.WriteString(patch)
	}
	return result.String(), nil
}
