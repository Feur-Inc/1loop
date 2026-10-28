package ui

// SSH machines (internal/sshmachine): Settings → SSH machines adds them,
// the target menu chooses one, and its projects are folders in its
// projects folder there. The agent keeps running here; its files,
// commands, terminal and Git changes are on the machine (agent.Machine).
// No connection is made on the interface's thread: trusting a new host key
// needs it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/platform"
	"1loop/internal/remoteapi"
	"1loop/internal/sshmachine"
)

// machineFor is the SSH machine of an ID, nil when it is not in Settings.
// Nothing connects until it is used.
func (ui *desktopUI) machineFor(id string) *sshmachine.Machine {
	if id == "" {
		return nil
	}
	if machine := ui.machines[id]; machine != nil {
		return machine
	}
	for _, settings := range ui.sshMachines {
		if settings.ID == id {
			if ui.machines == nil {
				ui.machines = map[string]*sshmachine.Machine{}
			}
			machine := sshmachine.New(settings, sshmachine.LoadSecrets()[id], ui.trustHost)
			ui.machines[id] = machine
			return machine
		}
	}
	return nil
}

// forgetMachine closes a machine's connections: its settings changed or
// it was removed.
func (ui *desktopUI) forgetMachine(id string) {
	if machine := ui.machines[id]; machine != nil {
		delete(ui.machines, id)
		go machine.Close()
	}
}

// trustHost asks the user about a host key seen for the first time.
func (ui *desktopUI) trustHost(ctx context.Context, machine, address, keyType, fingerprint string) bool {
	answer := make(chan bool, 1)
	fyne.Do(func() {
		var dialog *sheet
		reply := func(trusted bool) {
			select {
			case answer <- trusted:
			default:
			}
		}
		body := container.NewVBox(
			plainFlow("1loop has never connected to "+machine+" ("+address+"). Its host key is:", 13, colText),
			plainFlow(keyType+" "+fingerprint, 13, colText),
			plainFlow("Trust it only if it is the machine's key: on the machine, ssh-keygen -lf /etc/ssh/ssh_host_"+hostKeyFile(keyType)+"_key.pub shows it. 1loop remembers it and refuses the connection if it ever changes.", 12.5, colMuted))
		cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
		trust := newRow("Trust", nil, stylePrimary, func() { reply(true); dialog.dismiss() })
		dialog = ui.modal("Trust this SSH machine?", body, 560, func() { reply(false) }, cancel, trust)
	})
	select {
	case trusted := <-answer:
		return trusted
	case <-ctx.Done():
		return false
	}
}

// hostKeyFile is the name sshd gives the host key of this type.
func hostKeyFile(keyType string) string {
	switch {
	case strings.Contains(keyType, "ed25519"):
		return "ed25519"
	case strings.HasPrefix(keyType, "ecdsa"):
		return "ecdsa"
	}
	return "rsa"
}

func (ui *desktopUI) machineSettings(id string) (sshmachine.Settings, bool) {
	for _, settings := range ui.sshMachines {
		if settings.ID == id {
			return settings, true
		}
	}
	return sshmachine.Settings{}, false
}

// targetLabelOf names a machine, or this computer.
func (ui *desktopUI) targetLabelOf(id string) string {
	if settings, ok := ui.machineSettings(id); ok {
		return settings.Label()
	}
	return "This computer"
}

// targetLabel is what the target chip shows: this computer, or the machine.
func (ui *desktopUI) targetLabel() string { return ui.targetLabelOf(ui.cfg.MachineID) }

// useMachine makes an SSH machine the target: connected first, then its
// first project, or none until one is made.
func (ui *desktopUI) useMachine(id string) {
	machine := ui.machineFor(id)
	if machine == nil || ui.server == nil && ui.cfg.MachineID == id {
		return
	}
	ui.machine.SetText(machine.Label() + " · connecting…")
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 2*time.Minute)
		defer cancel()
		_, err := machine.ProjectsFolder(ctx)
		fyne.Do(func() {
			if err != nil {
				ui.machine.SetText(ui.targetLabel())
				ui.notice(machine.Label(), err.Error())
				return
			}
			if ui.server != nil {
				ui.useComputer()
			}
			if ui.cfg.MachineID == "" {
				ui.localWorkspace = ui.cfg.Workspace
			}
			workspace := ""
			if projects := ui.machineProjects[id]; len(projects) > 0 {
				workspace = projects[0]
			}
			platform.Devf("ui", "target: SSH machine %s, project %s", machine.Label(), workspace)
			ui.cfg.MachineID, ui.cfg.Workspace = id, workspace
			ui.ensureTargetProject(workspace)
			ui.machine.SetText(machine.Label())
			ui.current = nil
			ui.save()
			ui.startNew()
		})
	}()
}

// leaveMachine makes this computer the target again, on its last project.
func (ui *desktopUI) leaveMachine() {
	ui.cfg.MachineID, ui.cfg.Workspace = "", ui.lastLocalWorkspace()
	ui.machine.SetText("This computer")
	ui.current = nil
	ui.save()
	ui.startNew()
}

// lastLocalWorkspace is this computer's project to come back to.
func (ui *desktopUI) lastLocalWorkspace() string {
	if ui.localWorkspace != "" {
		return ui.localWorkspace
	}
	if len(ui.projects) > 0 {
		return ui.projects[0]
	}
	home, _ := os.UserHomeDir()
	return home
}

// pickStart is where a file picker of this computer opens: the project,
// or the home folder when the project is on an SSH machine.
func (ui *desktopUI) pickStart() string {
	if ui.cfg.MachineID != "" {
		home, _ := os.UserHomeDir()
		return home
	}
	return ui.cfg.Workspace
}

// projectFolderRow is Settings → General's project folder: on an SSH
// machine it is chosen in the sidebar, not with this computer's picker.
func (ui *desktopUI) projectFolderRow(change fyne.CanvasObject) fyne.CanvasObject {
	if ui.cfg.MachineID != "" {
		return settingRow("Project folder", ui.targetLabel()+": "+ui.cfg.Workspace+". Choose another project of the machine in the sidebar.", nil)
	}
	return settingRow("Project folder", ui.cfg.Workspace, change)
}

// targetProjects are the target's projects: this computer's, or the
// machine's (paths there).
func (ui *desktopUI) targetProjects() []string {
	if ui.cfg.MachineID != "" {
		return ui.machineProjects[ui.cfg.MachineID]
	}
	return ui.projects
}

func (ui *desktopUI) setTargetProjects(projects []string) {
	if ui.cfg.MachineID == "" {
		ui.projects = projects
		return
	}
	if ui.machineProjects == nil {
		ui.machineProjects = map[string][]string{}
	}
	ui.machineProjects[ui.cfg.MachineID] = projects
}

func (ui *desktopUI) ensureTargetProject(path string) {
	if ui.cfg.MachineID == "" {
		ui.ensureProject(path)
		return
	}
	ui.ensureMachineProject(ui.cfg.MachineID, path)
}

func (ui *desktopUI) ensureMachineProject(id, path string) {
	if path == "" {
		return
	}
	for _, project := range ui.machineProjects[id] {
		if project == path {
			return
		}
	}
	if ui.machineProjects == nil {
		ui.machineProjects = map[string][]string{}
	}
	ui.machineProjects[id] = append(ui.machineProjects[id], path)
}

// folderLabel is the project chip under the composer.
func (ui *desktopUI) folderLabel() string {
	if ui.cfg.Workspace == "" {
		return "No project"
	}
	return ui.projectName(ui.cfg.Workspace)
}

// projectName is a project as the sidebar names it.
func (ui *desktopUI) projectName(project string) string {
	if ui.cfg.MachineID != "" {
		return path.Base(project)
	}
	return filepath.Base(project)
}

// addMachineProject makes a project on the machine: a folder of its
// projects folder, made there unless it exists.
func (ui *desktopUI) addMachineProject() {
	id := ui.cfg.MachineID
	machine := ui.machineFor(id)
	if machine == nil {
		return
	}
	settings := machine.Settings()
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Project name")
	problem := plainFlow("", 12.5, colDanger)
	var dialog *sheet
	var create *tapRow
	apply := func() {
		name := strings.TrimSpace(entry.Text)
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00\r\n") {
			setFlow(problem, "Enter a folder name, without / or \\.")
			return
		}
		create.Disable()
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, time.Minute)
			defer cancel()
			folder, err := machine.ProjectsFolder(ctx)
			project := path.Join(folder, name)
			if err == nil {
				err = machine.MkdirAll(ctx, project)
			}
			fyne.Do(func() {
				create.Enable()
				if err != nil {
					setFlow(problem, err.Error())
					return
				}
				dialog.Hide()
				if ui.cfg.MachineID != id {
					return
				}
				ui.ensureMachineProject(id, project)
				ui.selectProject(project)
			})
		}()
	}
	entry.OnSubmitted = func(string) { apply() }
	cancel := newRow("Cancel", nil, styleOutline, func() { dialog.dismiss() })
	create = newRow("Create", nil, stylePrimary, apply)
	body := container.NewVBox(
		note("A folder in "+settings.Projects+" on "+machine.Label()+". An existing folder of that name is opened as it is."),
		entry, problem)
	dialog = ui.modal("New project on "+machine.Label(), body, 480, nil, cancel, create)
	ui.window.Canvas().Focus(entry)
}

// ---------------------------------------------------------------------------
// The panel: a terminal, the Files tab.

// lazyTerminal is a machine's terminal that connects in the background: the
// panel shows it at once, typing waits in a buffer until it is connected.
type lazyTerminal struct {
	ready   chan struct{}
	mu      sync.Mutex
	process interface {
		io.ReadWriteCloser
		Resize(int, int) error
		Wait() int
	}
	err      error
	pending  bytes.Buffer
	columns  int
	rows     int
	closed   bool
	onFailed func(error)
}

func newLazyTerminal(ctx context.Context, machine *sshmachine.Machine, dir string, onFailed func(error)) *lazyTerminal {
	lazy := &lazyTerminal{ready: make(chan struct{}), onFailed: onFailed}
	go func() {
		process, err := machine.Terminal(ctx, dir) // connects first, when needed
		lazy.mu.Lock()
		lazy.process, lazy.err = process, err
		if err == nil {
			if lazy.closed {
				process.Close()
			} else {
				if lazy.columns > 0 {
					process.Resize(lazy.columns, lazy.rows)
				}
				if lazy.pending.Len() > 0 {
					process.Write(lazy.pending.Bytes())
					lazy.pending.Reset()
				}
			}
		}
		lazy.mu.Unlock()
		close(lazy.ready)
		if err != nil && onFailed != nil {
			onFailed(err)
		}
	}()
	return lazy
}

func (lazy *lazyTerminal) Read(p []byte) (int, error) {
	<-lazy.ready
	if lazy.err != nil {
		return 0, io.EOF
	}
	return lazy.process.Read(p)
}

func (lazy *lazyTerminal) Write(p []byte) (int, error) {
	lazy.mu.Lock()
	defer lazy.mu.Unlock()
	switch {
	case lazy.err != nil:
		return 0, lazy.err
	case lazy.process == nil:
		lazy.pending.Write(p)
		return len(p), nil
	}
	return lazy.process.Write(p)
}

func (lazy *lazyTerminal) Resize(columns, rows int) error {
	lazy.mu.Lock()
	defer lazy.mu.Unlock()
	if lazy.process == nil {
		lazy.columns, lazy.rows = columns, rows
		return nil
	}
	return lazy.process.Resize(columns, rows)
}

func (lazy *lazyTerminal) Close() error {
	lazy.mu.Lock()
	defer lazy.mu.Unlock()
	lazy.closed = true
	if lazy.process != nil {
		return lazy.process.Close()
	}
	return nil
}

func (lazy *lazyTerminal) Wait() int {
	<-lazy.ready
	if lazy.err != nil {
		return -1
	}
	return lazy.process.Wait()
}

// machineFiles is the Files tab's source for a machine's project.
func (ui *desktopUI) machineFiles(machine *sshmachine.Machine, root string) fileSource {
	resolve := func(ctx context.Context, relative string) (string, error) {
		relative = strings.TrimPrefix(path.Clean("/"+relative), "/")
		if relative == "" {
			return root, nil
		}
		return agent.MachinePath(ctx, machine, root, relative)
	}
	return fileSource{
		name: path.Base(root),
		list: func(ctx context.Context, folder string) ([]remoteapi.FileEntry, error) {
			dir, err := resolve(ctx, folder)
			if err != nil {
				return nil, err
			}
			infos, err := machine.ReadDir(ctx, dir)
			if err != nil {
				return nil, fmt.Errorf("This folder cannot be read")
			}
			entries := []remoteapi.FileEntry{}
			for _, info := range infos {
				if info.Name() == ".git" {
					continue
				}
				full := path.Join(dir, info.Name())
				isDir := info.IsDir()
				if info.Mode()&fs.ModeSymlink != 0 { // listed as what it points to, inside the project only
					target, err := agent.MachinePath(ctx, machine, root, full)
					if err != nil {
						continue
					}
					if stat, err := machine.Lstat(ctx, target); err == nil {
						isDir, info = stat.IsDir(), stat
					}
				}
				entries = append(entries, remoteapi.FileEntry{Name: info.Name(), Path: strings.TrimPrefix(strings.TrimPrefix(full, root), "/"), Dir: isDir, Size: info.Size(), Modified: info.ModTime()})
			}
			sort.SliceStable(entries, func(i, j int) bool {
				if entries[i].Dir != entries[j].Dir {
					return entries[i].Dir
				}
				return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
			})
			return entries, nil
		},
		preview: func(ctx context.Context, file string) (remoteapi.FilePreview, error) {
			target, err := resolve(ctx, file)
			if err != nil {
				return remoteapi.FilePreview{}, err
			}
			info, err := machine.Lstat(ctx, target)
			if err != nil || !info.Mode().IsRegular() {
				return remoteapi.FilePreview{}, fmt.Errorf("This file cannot be read")
			}
			const limit = 512 << 10
			data, err := machine.ReadPrefix(ctx, target, limit)
			if err != nil {
				return remoteapi.FilePreview{}, fmt.Errorf("This file cannot be read")
			}
			preview := remoteapi.FilePreview{Path: file, Size: info.Size(), Truncated: info.Size() > limit}
			kind := http.DetectContentType(data)
			switch {
			case strings.HasPrefix(kind, "image/"):
				preview.Image = true
			case bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(trimHalfRune(data)):
				preview.Binary = true
			default:
				preview.Text = string(trimHalfRune(data))
			}
			return preview, nil
		},
		picture: func(ctx context.Context, file string) (image.Image, error) {
			target, err := resolve(ctx, file)
			if err != nil {
				return nil, err
			}
			data, err := machine.ReadFile(ctx, target)
			if err != nil {
				return nil, err
			}
			picture, _, err := image.Decode(bytes.NewReader(data))
			return picture, err
		},
		add: func(ctx context.Context, folder string, paths []string) error {
			dir, err := resolve(ctx, folder)
			if err != nil {
				return err
			}
			for _, local := range paths {
				data, err := os.ReadFile(local)
				if err == nil {
					err = machine.WriteFile(ctx, path.Join(dir, filepath.Base(local)), data, true)
				}
				if errors.Is(err, fs.ErrExist) || err != nil && strings.Contains(err.Error(), "exist") {
					err = fmt.Errorf("a file of that name is already there")
				}
				if err != nil {
					return fmt.Errorf("%s: %w", filepath.Base(local), err)
				}
			}
			return nil
		},
	}
}

func trimHalfRune(data []byte) []byte {
	for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
		data = data[:len(data)-1]
	}
	return data
}

// machineFilesTab is filesTab for a machine's project.
func (panel *toolPanel) machineFilesTab(machine *sshmachine.Machine, root string) panelTab {
	ui := panel.ui
	ctx, cancel := context.WithCancel(ui.ctx)
	actions := []fileAction{
		{label: "Download", icon: iconDocument, run: func(file remoteapi.FileEntry) {
			go func() {
				home, _ := os.UserHomeDir()
				dir := filepath.Join(home, "Downloads")
				data, err := machine.ReadFile(ctx, path.Join(root, file.Path))
				var saved string
				if err == nil {
					if err = os.MkdirAll(dir, 0755); err == nil {
						saved, err = remoteapi.SaveFile(dir, "", file.Name, bytes.NewReader(data))
					}
				}
				fyne.Do(func() {
					if err != nil {
						ui.notice("Download", err.Error())
					} else {
						ui.notice("Download", "Saved a copy in "+filepath.Join(dir, saved))
					}
				})
			}()
		}},
		{label: "Attach", icon: iconPlus, run: func(file remoteapi.FileEntry) {
			ui.fileAttachments = append(ui.fileAttachments, fileAttachment{Name: file.Name, Path: path.Join(root, file.Path), Workspace: root})
			ui.renderAttachments()
		}},
		{label: "Copy path", icon: iconCopy, run: func(file remoteapi.FileEntry) { fyne.CurrentApp().Clipboard().SetContent(file.Path) }},
	}
	pick := func(done func([]string)) {
		home, _ := os.UserHomeDir()
		ui.pick(pickRequest{title: "Add files to the project on " + machine.Label(), start: home, multiple: true}, done)
	}
	files := buildFileBrowser(ctx, ui.window, ui.machineFiles(machine, root), actions, pick)
	return panelTab{kind: "files", title: "Files", icon: iconFolder, cancel: cancel, workspace: root, content: files.content, files: files}
}

// ---------------------------------------------------------------------------
// Settings → SSH machines.

func (ui *desktopUI) sshSettings() fyne.CanvasObject {
	settings := ui.prefs
	var rows []fyne.CanvasObject
	for _, machine := range ui.sshMachines {
		machine := machine
		edit := newRow("Edit", iconChevronRight, styleChip, func() { ui.sshEditor(machine) })
		edit.iconAfter = true
		rows = append(rows, settingRow(machine.Label(), machine.User+"@"+machine.Address()+" · projects in "+machine.Projects, edit))
	}
	add := newRow("Add machine", iconPlus, stylePrimary, func() {
		ui.sshEditor(sshmachine.Settings{ID: core.Identifier(), Port: 22, Auth: "key", KeyPath: "~/.ssh/id_ed25519", Projects: "~/1loop-projects"})
	})
	page := container.NewVBox(settings.pageTitle("SSH machines", "", container.NewCenter(add)),
		padded(0, 10, 2, 0, plainFlow("Computers the agent can work on over SSH: choose one in the target menu, under the composer. Its projects are folders there; the agent edits files, runs commands and opens its terminal there, while this computer keeps its models, Browser Use and Computer Use.", 13, colMuted)))
	if len(rows) == 0 {
		rows = append(rows, settingRow("No machine yet", "Add one to work on it over SSH.", nil))
	}
	page.Add(card(rows...))
	return page
}

// sshEditor is a machine's page: saved as a whole, tested on demand.
func (ui *desktopUI) sshEditor(original sshmachine.Settings) {
	settings := ui.prefs
	_, existing := ui.machineSettings(original.ID)
	secrets := sshmachine.LoadSecrets()[original.ID]
	name := widget.NewEntry()
	name.SetText(original.Name)
	name.SetPlaceHolder("Optional: shown in the target menu")
	host := widget.NewEntry()
	host.SetText(original.Host)
	host.SetPlaceHolder("Host name or IP address")
	port := widget.NewEntry()
	if original.Port != 0 {
		port.SetText(strconv.Itoa(original.Port))
	}
	port.SetPlaceHolder("22")
	user := widget.NewEntry()
	user.SetText(original.User)
	password := widget.NewPasswordEntry()
	password.SetText(secrets.Password)
	keyPath := widget.NewEntry()
	keyPath.SetText(original.KeyPath)
	passphrase := widget.NewPasswordEntry()
	passphrase.SetText(secrets.Passphrase)
	passphrase.SetPlaceHolder("Only for a protected key")
	projects := widget.NewEntry()
	projects.SetText(original.Projects)
	status := plainFlow("", 13, colMuted)
	say := func(text string) { setFlow(status, text) }

	auth := newPicker(ui.window, "Sign in with", styleField, nil)
	auth.Options = []string{"Private key", "Password", "SSH agent"}
	authIDs := map[string]string{"Private key": "key", "Password": "password", "SSH agent": "agent"}
	browse := newRow("Choose…", nil, styleOutline, func() {
		home, _ := os.UserHomeDir()
		ui.pick(pickRequest{title: "Choose the private key", start: filepath.Join(home, ".ssh")}, func(paths []string) {
			keyPath.SetText(paths[0])
		})
	})
	passwordRow := settingRow("Password", "Kept in 1loop's settings folder, readable by your user only.", sized(340, password))
	keyRows := container.NewVBox(
		settingRow("Private key", "A key file of this computer, as ssh -i takes it.", container.NewBorder(nil, nil, nil, browse, sized(260, keyPath))),
		settingRow("Key passphrase", "Kept in 1loop's settings folder, readable by your user only.", sized(340, passphrase)))
	agentRow := settingRow("SSH agent", "Uses the keys of the agent of this session (SSH_AUTH_SOCK), as ssh does.", nil)
	access := container.NewVBox(passwordRow, keyRows, agentRow)
	showAccess := func(choice string) {
		passwordRow.Hide()
		keyRows.Hide()
		agentRow.Hide()
		switch authIDs[choice] {
		case "password":
			passwordRow.Show()
		case "key":
			keyRows.Show()
		default:
			agentRow.Show()
		}
		access.Refresh()
	}
	auth.onChanged = showAccess
	for label, id := range authIDs {
		if id == original.Auth {
			auth.SetSelected(label)
			showAccess(label)
		}
	}

	draft := func() (sshmachine.Settings, sshmachine.Secrets, error) {
		machine := original
		machine.Name = strings.TrimSpace(name.Text)
		machine.Host = strings.TrimSpace(host.Text)
		machine.User = strings.TrimSpace(user.Text)
		machine.Auth = authIDs[auth.Selected]
		machine.KeyPath = strings.TrimSpace(keyPath.Text)
		machine.Projects = strings.TrimRight(strings.TrimSpace(projects.Text), "/")
		if machine.Projects == "" && strings.TrimSpace(projects.Text) != "" {
			machine.Projects = "/"
		}
		machine.Port = 22
		if text := strings.TrimSpace(port.Text); text != "" {
			value, err := strconv.Atoi(text)
			if err != nil || value < 1 || value > 65535 {
				return machine, sshmachine.Secrets{}, fmt.Errorf("Enter a port from 1 to 65535")
			}
			machine.Port = value
		}
		secret := sshmachine.Secrets{}
		switch machine.Auth {
		case "password":
			secret.Password = password.Text
			machine.KeyPath = ""
		case "key":
			secret.Passphrase = passphrase.Text
		default:
			machine.KeyPath = ""
		}
		return machine, secret, machine.Check()
	}

	test := newRow("Test connection", nil, styleOutline, nil)
	test.onTap = func() {
		machine, secret, err := draft()
		if err != nil {
			say(err.Error())
			return
		}
		test.Disable()
		say("Connecting to " + machine.Address() + "…")
		go func() {
			ctx, cancel := context.WithTimeout(ui.ctx, 2*time.Minute)
			defer cancel()
			probe := sshmachine.New(machine, secret, ui.trustHost)
			defer probe.Close()
			result, err := probe.Check(ctx)
			fyne.Do(func() {
				test.Enable()
				if err != nil {
					say(err.Error())
					return
				}
				say(result)
			})
		}()
	}

	title := original.Label()
	if !existing {
		title = "New SSH machine"
	}
	page := container.NewVBox(
		settings.pageTitle(title, "SSH machines", nil),
		card(
			settingRow("Name", "As the target menu shows it.", sized(340, name)),
			settingRow("Host", "Its name or IP address.", sized(340, host)),
			settingRow("Port", "", sized(120, port)),
			settingRow("User", "The account to sign in to.", sized(340, user)),
			settingRow("Sign in with", "", sized(340, auth.row))),
		card(access),
		sectionTitle("Projects"),
		card(settingRow("Projects folder", "On the machine: a new project there is a folder in it. ~/ is the user's home folder.", sized(340, projects))),
		card(settingStack("Connection", "Connects with these settings, and asks you to trust the machine's host key the first time.", container.NewHBox(test))),
	)
	if existing {
		remove := newRow("Remove", iconTrash, styleOutline, func() {
			ui.confirm("Remove SSH machine?", "Remove “"+original.Label()+"” from 1loop? Its chats stay in 1loop; its files on the machine are not touched.", "Remove", func() {
				ui.removeMachine(original.ID)
				settings.done()
			})
		})
		remove.danger = true
		page.Add(sectionTitle("Remove"))
		page.Add(card(settingRow("Remove this machine", "Its projects and chats are kept but cannot run until it is added again.", remove)))
	}
	page.Add(spacer(0, 16))
	save := newRow("Save machine", nil, stylePrimary, func() {
		machine, secret, err := draft()
		if err != nil {
			say(err.Error())
			return
		}
		if err := sshmachine.SaveSecrets(machine.ID, secret); err != nil {
			say(err.Error())
			return
		}
		found := false
		for i := range ui.sshMachines {
			if ui.sshMachines[i].ID == machine.ID {
				ui.sshMachines[i], found = machine, true
			}
		}
		if !found {
			ui.sshMachines = append(ui.sshMachines, machine)
		}
		ui.forgetMachine(machine.ID) // the next use connects with the new settings
		if ui.cfg.MachineID == machine.ID {
			ui.machine.SetText(machine.Label())
		}
		platform.Devf("ui", "SSH machine %q saved", machine.Label())
		ui.save()
		settings.done()
	})
	cancel := newRow("Cancel", nil, styleOutline, settings.back)
	settings.push(settingsFrame{content: page, actions: []fyne.CanvasObject{container.NewCenter(sized(360, status)), cancel, save}})
}

// removeMachine forgets a machine: its chats stay, unable to run.
func (ui *desktopUI) removeMachine(id string) {
	kept := ui.sshMachines[:0]
	for _, machine := range ui.sshMachines {
		if machine.ID != id {
			kept = append(kept, machine)
		}
	}
	ui.sshMachines = kept
	ui.forgetMachine(id)
	sshmachine.SaveSecrets(id, sshmachine.Secrets{})
	if ui.server == nil && ui.cfg.MachineID == id {
		ui.leaveMachine()
	}
	ui.save()
}
