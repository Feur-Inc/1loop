// SPDX-License-Identifier: MPL-2.0

package server

// The admin socket: "1loop-server pair <id> <code>", "devices", "revoke <n>"
// (and the console's other commands) run in the server that is running, from
// another shell of the same user, such as "docker exec". The socket is a Unix
// socket in the private data directory (0700), itself 0600: only that user,
// and root, reach it, as they could the data directory's keys.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// adminSocketName is the socket's file in the data directory.
const adminSocketName = "admin.sock"

// adminCommands are the console's commands, which "1loop-server <command>"
// sends to the running server.
var adminCommands = map[string]bool{"pair": true, "devices": true, "revoke": true, "ssh": true, "trust": true, "refuse": true}

type adminReply struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// defaultDataDir is the data directory without -data-dir: ONELOOP_DATA_DIR
// (set in the Docker image, so "docker exec … 1loop-server pair" finds
// /data), or ~/.config/1loop-server.
func defaultDataDir() (string, error) {
	if dir := os.Getenv("ONELOOP_DATA_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "1loop-server"), nil
}

// listenAdmin opens the admin socket in dir and serves it until the
// returned function closes it.
func (server *headlessServer) listenAdmin(dir string) (func(), error) {
	path := filepath.Join(dir, adminSocketName)
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		conn.Close()
		return nil, fmt.Errorf("another 1loop server already uses %s", dir)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err // left by a server that stopped
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	// The directory is 0700 already, so nobody else reaches the socket
	// between Listen and Chmod.
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serveAdmin(conn)
		}
	}()
	return func() { listener.Close(); os.Remove(path) }, nil
}

// serveAdmin runs the one command a connection sends, as a line of
// space-separated words, and answers with its output.
func (server *headlessServer) serveAdmin(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(conn, 4096)).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	var output strings.Builder
	parts := strings.Fields(line)
	if len(parts) == 0 {
		parts = []string{"help"} // the list of commands
	}
	ok := server.command(parts, &output)
	json.NewEncoder(conn).Encode(adminReply{OK: ok, Output: output.String()})
}

// adminCommand sends one command to the server running with data directory
// dir, and prints its answer to out.
func adminCommand(dir string, args []string, out io.Writer) error {
	path := filepath.Join(dir, adminSocketName)
	conn, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return fmt.Errorf("no 1loop server is running with the data directory %s (%v): start it first, or give the -data-dir it uses", dir, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := fmt.Fprintln(conn, strings.Join(args, " ")); err != nil {
		return err
	}
	var reply adminReply
	if err := json.NewDecoder(conn).Decode(&reply); err != nil {
		return fmt.Errorf("the server did not answer: %v", err)
	}
	io.WriteString(out, reply.Output)
	if !reply.OK {
		return errAdminFailed
	}
	return nil
}

// errAdminFailed is a command the server refused: its output says why.
var errAdminFailed = errors.New("the command failed")

// pairCommand is the command that approves a pairing from the server's
// machine, as the pairing dialog shows it: with "docker exec" when the server
// runs in a container, whose hostname is the container's ID unless set.
func (server *headlessServer) pairCommand(id, code string) string {
	command := "1loop-server"
	if dir, err := defaultDataDir(); err != nil || filepath.Clean(dir) != filepath.Clean(server.dir) {
		command += " -data-dir " + shellQuote(server.dir)
	}
	command += " pair " + id + " " + code
	if engine := containerEngine(); engine != "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "<container>"
		}
		command = engine + " exec " + host + " " + command
	}
	return command
}

// containerEngine is "docker" or "podman" when the server runs in one of
// their containers, or "".
var containerEngine = func() string {
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return "podman"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	return ""
}

func shellQuote(text string) string {
	if text != "" && strings.Trim(text, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+:@") == "" {
		return text
	}
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}
