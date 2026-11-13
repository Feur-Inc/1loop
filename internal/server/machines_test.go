// SPDX-License-Identifier: MPL-2.0

package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"1loop/internal/agent"
	"1loop/internal/sshmachine"
)

func TestParseMachineSpec(t *testing.T) {
	machine, err := parseMachineSpec("mopi@host.docker.internal key=/run/secrets/1loop_ssh_key projects=~/dev")
	if err != nil {
		t.Fatal(err)
	}
	if machine.User != "mopi" || machine.Host != "host.docker.internal" || machine.Port != 22 || machine.Auth != "key" ||
		machine.KeyPath != "/run/secrets/1loop_ssh_key" || machine.Projects != "~/dev" || machine.HostKey != "" || !strings.HasPrefix(machine.ID, "ssh-") {
		t.Fatalf("parsed %+v", machine)
	}
	// Commas let a name have spaces; the port and a pinned key are read too.
	other, err := parseMachineSpec("mopi@host.docker.internal:2222, key=/k, name=The host, host-key=SHA256:abc")
	if err != nil {
		t.Fatal(err)
	}
	if other.Port != 2222 || other.Name != "The host" || other.Label() != "The host" || other.HostKey != "SHA256:abc" || other.Projects != "~" {
		t.Fatalf("parsed %+v", other)
	}
	if other.ID == machine.ID {
		t.Fatal("another port is another machine")
	}
	again, _ := parseMachineSpec("mopi@host.docker.internal,key=/other,projects=/srv")
	if again.ID != machine.ID {
		t.Fatal("the same user and address keeps its ID, so a restart with other options replaces the machine")
	}
	ipv6, err := parseMachineSpec("root@[fd00::1]:22 key=/k")
	if err != nil || ipv6.Host != "fd00::1" || ipv6.Port != 22 {
		t.Fatalf("IPv6: %+v, %v", ipv6, err)
	}
	for spec, problem := range map[string]string{
		"host.docker.internal key=/k":       "user@host",
		"mopi@ key=/k":                      "user@host",
		"mopi@host":                         "private key",
		"mopi@host:0 key=/k":                "port",
		"mopi@host:ssh key=/k":              "port",
		"mopi@host key=/k projects=dev":     "absolute path",
		"mopi@host key=/k host-key=abc":     "SHA256",
		"mopi@host key=/k sudo=yes":         "Unknown option",
		"mopi@host other@host key=/k":       "one user@host",
		"mopi@host/path key=/k":             "host alone",
		"mopi@host key=/k password=hunter2": "Unknown option",
	} {
		if _, err := parseMachineSpec(spec); err == nil || !strings.Contains(err.Error(), problem) {
			t.Errorf("%q: error %v, want one about %q", spec, err, problem)
		}
	}
	machines, err := machineSpecs("a@one key=/k; b@two key=/k ;", []string{"c@three key=/k"})
	if err != nil || len(machines) != 3 || machines[0].Host != "three" || machines[2].User != "b" {
		t.Fatalf("specs: %+v, %v", machines, err)
	}
	if _, err := machineSpecs("a@one", nil); err == nil {
		t.Fatal("a bad ONELOOP_SSH_MACHINE stops the server")
	}
}

// A host key seen for the first time waits for trust or refuse on the
// console; a pinned fingerprint needs no answer, and refuses another key.
func TestHostKeyConfirmation(t *testing.T) {
	var console bytes.Buffer
	var mu sync.Mutex
	trust := &hostTrust{out: writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return console.Write(p) })}
	printed := func() string { mu.Lock(); defer mu.Unlock(); return console.String() }

	pinned := trust.trustFunc("SHA256:good")
	if !pinned(context.Background(), "Host", "h:22", "ssh-ed25519", "SHA256:good") {
		t.Fatal("the pinned key is trusted")
	}
	if pinned(context.Background(), "Host", "h:22", "ssh-ed25519", "SHA256:evil") || !strings.Contains(printed(), "not the pinned SHA256:good") {
		t.Fatalf("another key is refused, and said: %q", printed())
	}

	ask := trust.trustFunc("")
	answers := make(chan bool, 1)
	go func() { answers <- ask(context.Background(), "Host", "h:22", "ssh-ed25519", "SHA256:new") }()
	request := waitRequest(t, trust)
	if !strings.Contains(printed(), "SHA256:new") || !strings.Contains(printed(), fmt.Sprintf("trust %d", request.number)) {
		t.Fatalf("the console shows the key and how to answer: %q", printed())
	}
	if trust.answer(request.number+1, true) {
		t.Fatal("an unknown number answers nothing")
	}
	if !trust.answer(request.number, true) || !<-answers {
		t.Fatal("trust accepts the key")
	}
	if len(trust.waiting()) != 0 || trust.answer(request.number, true) {
		t.Fatal("an answered key no longer waits")
	}

	go func() { answers <- ask(context.Background(), "Host", "h:22", "ssh-ed25519", "SHA256:new") }()
	request = waitRequest(t, trust)
	trust.answer(request.number, false)
	if <-answers {
		t.Fatal("refuse refuses the key")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { answers <- ask(ctx, "Host", "h:22", "ssh-ed25519", "SHA256:new") }()
	waitRequest(t, trust)
	cancel()
	if <-answers || len(trust.waiting()) != 0 {
		t.Fatal("a connection given up on refuses the key and stops waiting")
	}
}

func waitRequest(t *testing.T, trust *hostTrust) *hostKeyRequest {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if waiting := trust.waiting(); len(waiting) > 0 {
			return waiting[0]
		}
	}
	t.Fatal("no host key waits")
	return nil
}

type writerFunc func([]byte) (int, error)

func (write writerFunc) Write(p []byte) (int, error) { return write(p) }

// The headless server runs a project on an SSH machine set up from its
// console: the host key is confirmed there, the phone adds a folder of the
// machine as a project and sees which machine it is on, and the agent's
// command runs on the machine.
func TestProjectOnSSHMachine(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	keyFile, address, home := fakeSSHMachine(t)
	sshmachine.KnownHostsFile = filepath.Join(t.TempDir(), "known_hosts")
	t.Cleanup(func() { sshmachine.KnownHostsFile = "" })

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"mock"}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if !strings.Contains(string(body), "made on the machine") { // the first turn: a command
			if strings.Contains(string(body), "proj-secret") {
				t.Error("the project's variable was sent to the model")
			}
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"shell","arguments":"{\"command\":\"echo made on the machine \\\"$MADE_WITH\\\" > made.txt && cat made.txt\"}"}}]}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"Done."}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(model.Close)

	server, call := pairedServer(t)
	var console bytes.Buffer
	var mu sync.Mutex
	server.trust.out = writerFunc(func(p []byte) (int, error) { mu.Lock(); defer mu.Unlock(); return console.Write(p) })
	printed := func() string { mu.Lock(); defer mu.Unlock(); return console.String() }
	server.mu.Lock()
	server.approvalMode = "yolo"
	server.connections = []agent.Connection{{ID: "mock", Name: "Mock", Provider: "compatible", BaseURL: model.URL + "/v1", Enabled: true, AllModels: true}}
	server.cfg = agent.Config{Provider: "compatible", BaseURL: model.URL + "/v1", Model: "mock", ConnectionID: "mock"}
	server.mu.Unlock()

	console1 := func(line string) {
		server.mu.Lock()
		defer server.mu.Unlock()
		server.machineConsole(strings.Fields(line))
	}
	console1("ssh add tester@" + address + " key=" + keyFile + " projects=" + home + " name=Host")
	request := waitRequest(t, server.trust)
	console1(fmt.Sprintf("trust %d", request.number))
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(printed(), "SSH machine Host: Connected"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("not connected: %q", printed())
		}
	}
	if data, err := os.ReadFile(sshmachine.KnownHostsFile); err != nil || !strings.Contains(string(data), "ssh-ed25519") {
		t.Fatalf("the trusted key is pinned: %q, %v", data, err)
	}
	machineID := server.machines[0].ID

	code, body := call("GET", "/api/projects?machine="+machineID, nil, true)
	if code != 200 || !strings.Contains(body, `"path":"`+home+`"`) {
		t.Fatalf("browse the machine: %d %s", code, body)
	}
	code, body = call("POST", "/api/projects", map[string]any{"path": filepath.Join(home, "..", "escape"), "create": true, "machine": machineID}, true)
	if code != 400 {
		t.Fatalf("a folder outside the machine's projects folder: %d %s", code, body)
	}
	code, body = call("POST", "/api/projects", map[string]any{"path": "demo", "create": true, "machine": machineID}, true)
	project := filepath.Join(home, "demo")
	if code != 200 || !strings.Contains(body, `"workspace":"`+project+`"`) {
		t.Fatalf("add a project on the machine: %d %s", code, body)
	}
	code, body = call("GET", "/api/state", nil, true)
	var state struct {
		ProjectMachines map[string]string
		Machines        []map[string]string
	}
	json.Unmarshal([]byte(body), &state)
	if code != 200 || state.ProjectMachines[project] != machineID || len(state.Machines) != 1 || state.Machines[0]["label"] != "Host" {
		t.Fatalf("the phone sees which machine the project is on: %d %s", code, body)
	}
	if strings.Contains(body, keyFile) {
		t.Fatal("the key file stays on the server")
	}
	if code, body = call("GET", "/api/files?workspace="+project, nil, true); code != 409 {
		t.Fatalf("the file browser refuses the machine's folder: %d %s", code, body)
	}

	// The project's variables (#408) reach its commands on the machine.
	if code, body = call("POST", "/api/projects/env", map[string]any{"path": project, "env": map[string]string{"MADE_WITH": "proj-secret it's"}}, true); code != 200 {
		t.Fatalf("set the project's variables: %d %s", code, body)
	}
	code, body = call("POST", "/api/send", map[string]any{"text": "make a file", "workspace": project}, true)
	if code != 200 {
		t.Fatalf("send: %d %s", code, body)
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		server.mu.Lock()
		done := len(server.chats) > 0 && server.chats[0].run == nil && len(server.chats[0].History) >= 3
		server.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run did not finish")
		}
	}
	if data, err := os.ReadFile(filepath.Join(project, "made.txt")); err != nil || string(data) != "made on the machine proj-secret it's\n" {
		t.Fatalf("the command ran on the machine: %q, %v", data, err)
	}
	code, body = call("GET", "/api/state", nil, true)
	if !strings.Contains(body, `"machine":"Host"`) {
		t.Fatalf("the chat says its machine: %s", body)
	}

	// Removing the machine forgets its projects; their chats stay, unable to run.
	console1("ssh remove 1")
	server.mu.Lock()
	projects, chats := len(server.projectMachines), len(server.chats)
	server.mu.Unlock()
	if projects != 0 || chats != 1 {
		t.Fatalf("after ssh remove: %d machine projects, %d chats", projects, chats)
	}
	server.mu.Lock()
	chat := server.chats[0].ID
	server.mu.Unlock()
	if code, body = call("POST", "/api/send", map[string]any{"text": "again", "chat": chat}, true); code == 200 || !strings.Contains(body, "SSH machine") {
		t.Fatalf("a chat whose machine is gone: %d %s", code, body)
	}
}

// fakeSSHMachine is an SSH server on loopback that runs commands with this
// computer's sh and serves its files over SFTP, as tester with a key.
func fakeSSHMachine(t *testing.T) (keyFile, address, home string) {
	t.Helper()
	home, _ = filepath.EvalSymlinks(t.TempDir())
	_, hostPrivate, _ := ed25519.GenerateKey(rand.Reader)
	hostSigner, _ := ssh.NewSignerFromKey(hostPrivate)
	userPublic, userPrivate, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(userPrivate, "")
	if err != nil {
		t.Fatal(err)
	}
	keyFile = filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	authorized, _ := ssh.NewPublicKey(userPublic)
	config := &ssh.ServerConfig{PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if meta.User() == "tester" && bytes.Equal(key.Marshal(), authorized.Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("refused")
	}}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveSSH(conn, config, home)
		}
	}()
	return keyFile, listener.Addr().String(), home
}

func serveSSH(conn net.Conn, config *ssh.ServerConfig, home string) {
	server, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		conn.Close()
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range requests {
				switch request.Type {
				case "subsystem":
					request.Reply(true, nil)
					files, err := sftp.NewServer(channel, sftp.WithServerWorkingDirectory(home))
					if err == nil {
						files.Serve()
					}
					return
				case "exec":
					request.Reply(true, nil)
					length := binary.BigEndian.Uint32(request.Payload)
					command := exec.Command("sh", "-c", string(request.Payload[4:4+length]))
					command.Dir = home
					command.Stdout, command.Stderr = channel, channel.Stderr()
					input, _ := command.StdinPipe()
					// The client keeps its input open while it waits: copied apart, not awaited.
					go func() { io.Copy(input, channel); input.Close() }()
					status := 0
					if err := command.Run(); err != nil {
						status = 1
						if exit, ok := err.(*exec.ExitError); ok {
							status = exit.ExitCode()
						}
					}
					channel.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, uint32(status)))
					return
				default:
					if request.WantReply {
						request.Reply(false, nil)
					}
				}
			}
		}()
	}
}
