// SPDX-License-Identifier: MPL-2.0

package server

// SSH machines of the headless server (internal/sshmachine). A project can be
// a folder of one, so the agent's files, commands and terminals are on that
// computer, often the server's own host reached from its container, while the
// server itself stays where it runs. They are set up headless: -ssh-machine
// (or ONELOOP_SSH_MACHINE) and the console's ssh command. A host key seen for
// the first time waits for trust <number> on the console, unless the machine
// pins its fingerprint (host-key=).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"1loop/internal/agent"
	"1loop/internal/core"
	"1loop/internal/remoteapi"
	"1loop/internal/sshmachine"
	"1loop/internal/terminal"
)

// serverMachine is an SSH machine as the server keeps it.
type serverMachine struct {
	sshmachine.Settings
	// HostKey pins the SHA256 fingerprint of the machine's host key: the
	// first connection then needs no confirmation on the console.
	HostKey string `json:"hostKey,omitempty"`
}

const machineSpecUsage = "user@host[:port] key=FILE [projects=DIR] [name=NAME] [host-key=SHA256:…]"

// parseMachineSpec reads a machine as -ssh-machine and the console's ssh add
// give it: user@host[:port] then options, separated by commas, or by spaces
// when there is no comma (a name with spaces needs the commas).
func parseMachineSpec(spec string) (serverMachine, error) {
	split := unicode.IsSpace
	if strings.Contains(spec, ",") {
		split = func(r rune) bool { return r == ',' }
	}
	machine := serverMachine{Settings: sshmachine.Settings{Auth: "key", Projects: "~"}}
	destination := ""
	for _, field := range strings.FieldsFunc(spec, split) {
		field = strings.TrimSpace(field)
		name, value, option := strings.Cut(field, "=")
		if !option {
			if destination != "" {
				return serverMachine{}, fmt.Errorf("Give one user@host, then options: %s", machineSpecUsage)
			}
			destination = field
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "key":
			machine.KeyPath = value
		case "projects":
			machine.Projects = value
		case "name":
			machine.Name = value
		case "host-key":
			if !strings.HasPrefix(value, "SHA256:") || len(value) <= len("SHA256:") {
				return serverMachine{}, fmt.Errorf("Give host-key as ssh-keygen -lf shows it: SHA256:…")
			}
			machine.HostKey = value
		default:
			return serverMachine{}, fmt.Errorf("Unknown option %q: %s", name, machineSpecUsage)
		}
	}
	at := strings.LastIndex(destination, "@")
	if at <= 0 || at == len(destination)-1 {
		return serverMachine{}, fmt.Errorf("Give the machine as user@host[:port]: %s", machineSpecUsage)
	}
	machine.User, machine.Host = destination[:at], destination[at+1:]
	if host, port, err := net.SplitHostPort(machine.Host); err == nil {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return serverMachine{}, fmt.Errorf("Give a port from 1 to 65535")
		}
		machine.Host, machine.Port = host, number
	} else {
		machine.Host = strings.TrimSuffix(strings.TrimPrefix(machine.Host, "["), "]")
	}
	if machine.Port == 0 {
		machine.Port = 22
	}
	if machine.KeyPath == "" {
		return serverMachine{}, fmt.Errorf("Give the private key file, for example a Docker secret: key=/run/secrets/1loop_ssh_key")
	}
	if err := machine.Check(); err != nil {
		return serverMachine{}, err
	}
	// The same user and address is the same machine, whatever its options.
	sum := sha256.Sum256([]byte(machine.User + "@" + machine.Address()))
	machine.ID = "ssh-" + hex.EncodeToString(sum[:6])
	return machine, nil
}

// machineSpecs are the machines of ONELOOP_SSH_MACHINE (several separated by
// ;) and of the -ssh-machine flags.
func machineSpecs(env string, flags []string) ([]serverMachine, error) {
	specs := append([]string{}, flags...)
	for _, spec := range strings.Split(env, ";") {
		if strings.TrimSpace(spec) != "" {
			specs = append(specs, spec)
		}
	}
	machines := []serverMachine{}
	for _, spec := range specs {
		machine, err := parseMachineSpec(spec)
		if err != nil {
			return nil, fmt.Errorf("SSH machine %q: %w", spec, err)
		}
		machines = append(machines, machine)
	}
	return machines, nil
}

// ---------------------------------------------------------------------------
// Host keys: pinned, or confirmed on the console.

type hostKeyRequest struct {
	number                                 int
	machine, address, keyType, fingerprint string
	answer                                 chan bool
}

type hostTrust struct {
	mu      sync.Mutex
	next    int
	pending map[int]*hostKeyRequest
	out     io.Writer // the console
}

// trustFunc confirms a machine's first host key: by its pinned fingerprint,
// else by the user on the console, who trust <number> or refuse <number> it.
// sshmachine keeps a trusted key and refuses the machine if it ever changes.
func (trust *hostTrust) trustFunc(pinned string) sshmachine.TrustFunc {
	return func(ctx context.Context, machine, address, keyType, fingerprint string) bool {
		if pinned != "" {
			if fingerprint != pinned {
				fmt.Fprintf(trust.out, "SSH machine %s (%s) presents host key %s %s, not the pinned %s: refused.\n", machine, address, keyType, fingerprint, pinned)
				return false
			}
			return true
		}
		trust.mu.Lock()
		trust.next++
		request := &hostKeyRequest{number: trust.next, machine: machine, address: address, keyType: keyType, fingerprint: fingerprint, answer: make(chan bool, 1)}
		if trust.pending == nil {
			trust.pending = map[int]*hostKeyRequest{}
		}
		trust.pending[request.number] = request
		trust.mu.Unlock()
		defer func() {
			trust.mu.Lock()
			delete(trust.pending, request.number)
			trust.mu.Unlock()
		}()
		fmt.Fprintf(trust.out, "SSH machine %s (%s) has never been connected to. Its host key is %s %s.\n"+
			"Trust it only if it is the machine's key: there, ssh-keygen -lf /etc/ssh/ssh_host_%s_key.pub shows it.\n"+
			"Type trust %d to accept it, refuse %d otherwise.\n",
			machine, address, keyType, fingerprint, hostKeyFile(keyType), request.number, request.number)
		select {
		case trusted := <-request.answer:
			return trusted
		case <-ctx.Done():
			return false
		}
	}
}

// answer replies to a waiting host key; false when none has this number.
func (trust *hostTrust) answer(number int, trusted bool) bool {
	trust.mu.Lock()
	defer trust.mu.Unlock()
	request := trust.pending[number]
	if request == nil {
		return false
	}
	delete(trust.pending, number)
	request.answer <- trusted
	return true
}

// waiting are the host keys waiting for an answer, oldest first.
func (trust *hostTrust) waiting() []*hostKeyRequest {
	trust.mu.Lock()
	defer trust.mu.Unlock()
	requests := []*hostKeyRequest{}
	for _, request := range trust.pending {
		requests = append(requests, request)
	}
	sort.Slice(requests, func(i, j int) bool { return requests[i].number < requests[j].number })
	return requests
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

// ---------------------------------------------------------------------------
// The server's machines.

// machineLocked is the machine of an ID, nil when the server has none.
// Nothing connects until it is used.
func (server *headlessServer) machineLocked(id string) *sshmachine.Machine {
	if id == "" {
		return nil
	}
	if machine := server.connected[id]; machine != nil {
		return machine
	}
	for _, settings := range server.machines {
		if settings.ID == id {
			if server.connected == nil {
				server.connected = map[string]*sshmachine.Machine{}
			}
			machine := sshmachine.New(settings.Settings, sshmachine.Secrets{}, server.trust.trustFunc(settings.HostKey))
			server.connected[id] = machine
			return machine
		}
	}
	return nil
}

// projectMachineLocked is the machine a project is on, nil for the server's own.
func (server *headlessServer) projectMachineLocked(workspace string) *sshmachine.Machine {
	return server.machineLocked(server.projectMachines[workspace])
}

func (server *headlessServer) machineLabelLocked(id string) string {
	for _, machine := range server.machines {
		if machine.ID == id {
			return machine.Label()
		}
	}
	return ""
}

// addMachineLocked adds a machine, or replaces the one of the same ID
// (whose next use connects with the new settings), and saves it.
func (server *headlessServer) addMachineLocked(machine serverMachine) error {
	before := append([]serverMachine{}, server.machines...)
	index := slices.IndexFunc(server.machines, func(saved serverMachine) bool { return saved.ID == machine.ID })
	if index >= 0 {
		server.machines[index] = machine
	} else {
		server.machines = append(server.machines, machine)
	}
	if err := server.saveSettingsLocked(); err != nil {
		server.machines = before
		return err
	}
	server.forgetConnectionLocked(machine.ID)
	server.broadcastLocked("projects", server.projectsReplyLocked())
	return nil
}

// removeMachineLocked forgets a machine and its projects: their chats stay,
// unable to run.
func (server *headlessServer) removeMachineLocked(id string) error {
	machines := slices.DeleteFunc(append([]serverMachine{}, server.machines...), func(machine serverMachine) bool { return machine.ID == id })
	projects := slices.DeleteFunc(append([]string{}, server.projects...), func(project string) bool { return server.projectMachines[project] == id })
	before, beforeProjects, beforeMap, beforeWorkspace := server.machines, server.projects, server.projectMachines, server.cfg.Workspace
	server.machines, server.projects = machines, projects
	server.projectMachines = map[string]string{}
	for project, machine := range beforeMap {
		if machine != id {
			server.projectMachines[project] = machine
		}
	}
	if !slices.Contains(server.projects, server.cfg.Workspace) && server.worktrees[server.cfg.Workspace] == "" {
		server.cfg.Workspace = ""
		if len(server.projects) > 0 {
			server.cfg.Workspace = server.projects[0]
		}
	}
	if err := server.saveSettingsLocked(); err != nil {
		server.machines, server.projects, server.projectMachines, server.cfg.Workspace = before, beforeProjects, beforeMap, beforeWorkspace
		return err
	}
	server.forgetConnectionLocked(id)
	server.broadcastLocked("projects", server.projectsReplyLocked())
	return nil
}

func (server *headlessServer) forgetConnectionLocked(id string) {
	if machine := server.connected[id]; machine != nil {
		delete(server.connected, id)
		go machine.Close()
	}
}

// checkMachine connects to a machine and says on the console how it went:
// a new host key is confirmed there now, not when a project first runs.
func (server *headlessServer) checkMachine(id string) {
	server.mu.Lock()
	machine := server.machineLocked(id)
	server.mu.Unlock()
	if machine == nil {
		return
	}
	ctx, cancel := context.WithTimeout(server.ctx, 10*time.Minute)
	defer cancel()
	result, err := machine.Check(ctx)
	if err != nil {
		fmt.Fprintf(server.trust.out, "SSH machine %s: %v\n", machine.Label(), err)
		return
	}
	fmt.Fprintf(server.trust.out, "SSH machine %s: %s\n", machine.Label(), result)
}

// machinesLocked is what clients show of the machines: never a key path.
func (server *headlessServer) machinesLocked() []map[string]string {
	machines := []map[string]string{}
	for _, machine := range server.machines {
		machines = append(machines, map[string]string{"id": machine.ID, "label": machine.Label(), "projects": machine.Projects})
	}
	return machines
}

// projectsReplyLocked is the projects event: the folders, their worktrees and
// the machines they are on.
func (server *headlessServer) projectsReplyLocked() map[string]any {
	return map[string]any{
		"projects":        server.projects,
		"worktrees":       server.worktrees,
		"machines":        server.machinesLocked(),
		"projectMachines": server.projectMachinesLocked(),
	}
}

func (server *headlessServer) projectMachinesLocked() map[string]string {
	machines := map[string]string{}
	for project, id := range server.projectMachines {
		machines[project] = id
	}
	return machines
}

// machineFolder is a folder of the machine within its projects folder, ~/
// resolved, made first when create.
func machineFolder(ctx context.Context, machine *sshmachine.Machine, requested string, create bool) (string, string, error) {
	base, err := machine.ProjectsFolder(ctx)
	if err != nil {
		return "", "", err
	}
	folder := base
	if requested != "" {
		folder = path.Clean(machine.Path(requested))
		if !path.IsAbs(folder) {
			folder = path.Join(base, folder)
		}
	}
	if folder != base && !strings.HasPrefix(folder, strings.TrimSuffix(base, "/")+"/") {
		return "", "", fmt.Errorf("Choose a folder in %s", base)
	}
	if create {
		if _, err := machine.Lstat(ctx, folder); err == nil {
			return "", "", fmt.Errorf("%s already exists", path.Base(folder))
		}
		if err := machine.MkdirAll(ctx, folder); err != nil {
			return "", "", err
		}
	}
	info, err := machine.Lstat(ctx, folder)
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		var real string
		if real, err = machine.RealPath(ctx, folder); err == nil {
			folder = real
			_, err = machine.Lstat(ctx, folder)
		}
		if err == nil && folder != base && !strings.HasPrefix(folder, strings.TrimSuffix(base, "/")+"/") {
			return "", "", fmt.Errorf("Choose a folder in %s", base)
		}
		if err == nil {
			info, err = machine.Lstat(ctx, folder)
		}
	}
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("Choose a folder")
	}
	return folder, base, nil
}

// machineProjectFolders lists a machine's folders as GET /api/projects does
// the server's own.
func machineProjectFolders(ctx context.Context, machine *sshmachine.Machine, requested string) (map[string]any, error) {
	folder, base, err := machineFolder(ctx, machine, requested, false)
	if err != nil {
		return nil, err
	}
	entries, err := machine.ReadDir(ctx, folder)
	if err != nil {
		return nil, err
	}
	folders := []string{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") && entry.IsDir() {
			folders = append(folders, path.Join(folder, entry.Name()))
		}
	}
	sort.Strings(folders)
	parent := ""
	if folder != base {
		parent = path.Dir(folder)
	}
	return map[string]any{"path": folder, "parent": parent, "base": base, "separator": "/", "folders": folders}, nil
}

// requestMachine is the machine a request names (?machine=), nil for none.
func (server *headlessServer) requestMachine(w http.ResponseWriter, id string) (*sshmachine.Machine, bool) {
	if id == "" {
		return nil, true
	}
	server.mu.Lock()
	machine := server.machineLocked(id)
	server.mu.Unlock()
	if machine == nil {
		core.Fail(w, 404, "This SSH machine is no longer on the server")
		return nil, false
	}
	return machine, true
}

// addMachineProject registers a folder of a machine as a project.
func (server *headlessServer) addMachineProject(w http.ResponseWriter, r *http.Request, id, requested string, create bool, env map[string]string) {
	machine, ok := server.requestMachine(w, id)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	folder, _, err := machineFolder(ctx, machine, requested, create)
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if owner, known := server.projectMachines[folder]; known && owner != id || !known && slices.Contains(server.projects, folder) {
		core.Fail(w, 409, "Another project already has the folder "+folder)
		return
	}
	if listed := slices.Contains(server.projects, folder); !listed || env != nil {
		before, previous := server.projectMachines, server.projectEnv[folder]
		if !listed {
			server.projects = append(server.projects, folder)
			server.projectMachines = server.projectMachinesLocked()
			server.projectMachines[folder] = id
		}
		if env != nil {
			server.setProjectEnvLocked(folder, env)
		}
		if err := server.saveSettingsLocked(); err != nil {
			if !listed {
				server.projects = server.projects[:len(server.projects)-1]
			}
			server.projectMachines = before
			server.setProjectEnvLocked(folder, previous)
			core.Fail(w, 500, "Unable to save the project")
			return
		}
	}
	reply := server.projectsReplyLocked()
	server.broadcastLocked("projects", reply)
	reply["workspace"] = folder
	core.WriteJSON(w, 200, reply)
}

// machineConsole is the console's ssh, trust and refuse commands.
func (server *headlessServer) machineConsole(parts []string) {
	out := server.trust.out
	switch {
	case parts[0] == "trust" || parts[0] == "refuse":
		number, err := strconv.Atoi(strings.Join(parts[1:], ""))
		if len(parts) != 2 || err != nil || !server.trust.answer(number, parts[0] == "trust") {
			fmt.Fprintf(out, "Usage: %s <number of a host key waiting, from ssh>\n", parts[0])
			return
		}
		if parts[0] == "trust" {
			fmt.Fprintln(out, "Host key trusted.")
		} else {
			fmt.Fprintln(out, "Host key refused.")
		}
	case len(parts) == 1:
		if len(server.machines) == 0 {
			fmt.Fprintf(out, "No SSH machines. Add one with: ssh add %s\n", machineSpecUsage)
		}
		for index, machine := range server.machines {
			projects := 0
			for _, id := range server.projectMachines {
				if id == machine.ID {
					projects++
				}
			}
			fmt.Fprintf(out, "%d  %s  %s@%s  key %s  projects in %s  (%d projects)\n", index+1, machine.Label(), machine.User, machine.Address(), machine.KeyPath, machine.Projects, projects)
		}
		for _, request := range server.trust.waiting() {
			fmt.Fprintf(out, "Waiting: host key %s %s of %s (%s): trust %d or refuse %d\n", request.keyType, request.fingerprint, request.machine, request.address, request.number, request.number)
		}
	case parts[1] == "add" && len(parts) > 2:
		machine, err := parseMachineSpec(strings.Join(parts[2:], " "))
		if err == nil {
			err = server.addMachineLocked(machine)
		}
		if err != nil {
			fmt.Fprintln(out, err)
			return
		}
		fmt.Fprintf(out, "Added %s. Connecting…\n", machine.Label())
		go server.checkMachine(machine.ID)
	case parts[1] == "remove" && len(parts) == 3:
		index, err := strconv.Atoi(parts[2])
		if err != nil || index < 1 || index > len(server.machines) {
			fmt.Fprintln(out, "Unknown SSH machine: ssh lists them")
			return
		}
		machine := server.machines[index-1]
		if err := server.removeMachineLocked(machine.ID); err != nil {
			fmt.Fprintln(out, err)
			return
		}
		fmt.Fprintf(out, "Removed %s and its projects; their files stay on it.\n", machine.Label())
	default:
		fmt.Fprintf(out, "Usage: ssh, ssh add %s, ssh remove <number>\n", machineSpecUsage)
	}
}

// machineFlags collects repeated -ssh-machine flags.
type machineFlags []string

func (flags *machineFlags) String() string         { return strings.Join(*flags, "; ") }
func (flags *machineFlags) Set(value string) error { *flags = append(*flags, value); return nil }

// useStartupMachines adds the machines of the flags and the environment,
// replacing saved ones of the same user and address, and connects to them.
func (server *headlessServer) useStartupMachines(machines []serverMachine) error {
	server.mu.Lock()
	defer server.mu.Unlock()
	for _, machine := range machines {
		if err := server.addMachineLocked(machine); err != nil {
			return err
		}
	}
	return nil
}

// checkMachines connects to every machine, so a new host key is asked about
// at startup.
func (server *headlessServer) checkMachines() {
	server.mu.Lock()
	ids := []string{}
	for _, machine := range server.machines {
		ids = append(ids, machine.ID)
	}
	server.mu.Unlock()
	for _, id := range ids {
		go server.checkMachine(id)
	}
}

// machineAttachedMessage checks a machine project's attachments as
// remoteapi.AttachedMessage does the server's own.
func machineAttachedMessage(ctx context.Context, machine agent.Machine, workspace, text string, files []agent.FileAttachment) (agent.Message, error) {
	if len(files) > 20 {
		return agent.Message{Role: "user", Content: text}, fmt.Errorf("Attach at most 20 files per message")
	}
	validated := make([]agent.FileAttachment, 0, len(files))
	var images []core.ImagePart
	imageBytes := 0
	for _, file := range files {
		if path.Clean(file.Workspace) != path.Clean(workspace) {
			return agent.Message{}, fmt.Errorf("Attached files belong to another workspace")
		}
		name, err := agent.MachinePath(ctx, machine, workspace, file.Path)
		if err != nil {
			return agent.Message{}, err
		}
		info, err := machine.Lstat(ctx, name)
		if err != nil {
			return agent.Message{}, err
		}
		if !info.Mode().IsRegular() {
			return agent.Message{}, fmt.Errorf("Choose a regular file")
		}
		file.Path, file.Name = name, path.Base(strings.ReplaceAll(file.Name, `\`, "/"))
		if strings.ContainsAny(file.Name, "\r\n\x00") {
			return agent.Message{}, fmt.Errorf("Invalid filename")
		}
		validated = append(validated, file)
		// As on the server's own projects, a small picture is shown to the model.
		if info.Size() <= 8<<20 && imageBytes+int(info.Size()) <= 16<<20 {
			data, err := machine.ReadFile(ctx, name)
			if err != nil {
				return agent.Message{}, err
			}
			if part, ok := remoteapi.AttachmentImage(data); ok {
				imageBytes += len(data)
				images = append(images, part)
				validated[len(validated)-1].Image = true
			}
		}
	}
	return agent.PromptMessage(text, images, validated), nil
}

// uploadMachineAttachment puts a file the phone attached in the machine
// project's tmp folder.
func uploadMachineAttachment(w http.ResponseWriter, r *http.Request, machine agent.Machine, workspace string) {
	r.Body = http.MaxBytesReader(w, r.Body, remoteapi.AttachmentLimit)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	file, err := agent.CopyAttachmentTo(ctx, machine, workspace, r.URL.Query().Get("name"), r.Body)
	if err != nil {
		core.Fail(w, 400, err.Error())
		return
	}
	core.WriteJSON(w, 200, file)
}

// machineTerminal is a terminal of the machine, in dir there.
func machineTerminal(parent context.Context, machine *sshmachine.Machine, dir string) (*serverTerminal, error) {
	return startServerTerminal(parent, dir, func(ctx context.Context) (terminal.Process, error) { return machine.Terminal(ctx, dir) })
}

// noMachineFiles refuses what reaches only the server's own folders.
func noMachineFiles(w http.ResponseWriter, label string) {
	core.Fail(w, 409, "This project is on "+label+": the file browser shows the server's own projects only. Open a terminal there, or ask the agent")
}
