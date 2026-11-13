// SPDX-License-Identifier: MPL-2.0

package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"1loop/internal/agent"
	"1loop/internal/browser"
)

// The slim image's Install Chromium and Install Claude Code: the server
// runs the install in the background, reports its progress and errors in
// /api/state, and then reports the browser and Claude Code it now has.
func TestInstallRoutes(t *testing.T) {
	t.Setenv("ONELOOP_BROWSER", "none")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	server, _, call := testServer(t)
	if agent.ClaudeInstallDir == "" || browser.ChromiumDir() == "" {
		t.Fatal("the server installs into its data folder")
	}
	release := make(chan struct{})
	saved := installers
	t.Cleanup(func() { installers = saved })
	fail := true
	installers = map[string]func(context.Context, func(string)) error{
		"chromium": func(ctx context.Context, progress func(string)) error {
			progress("Downloading Chromium… 50%")
			<-release
			if fail {
				return errors.New("storage.googleapis.com answered 503")
			}
			path := filepath.Join(browser.ChromiumDir(), "150.0.1.2", "chrome-linux64", "chrome")
			os.MkdirAll(filepath.Dir(path), 0755)
			os.WriteFile(filepath.Join(browser.ChromiumDir(), "checked"), nil, 0600)
			return os.WriteFile(path, []byte("#!/bin/sh\n"), 0755)
		},
		"claude-code": func(ctx context.Context, progress func(string)) error {
			os.MkdirAll(agent.ClaudeInstallDir, 0755)
			return os.WriteFile(filepath.Join(agent.ClaudeInstallDir, "claude"), []byte("#!/bin/sh\n"), 0755)
		},
	}
	type state struct {
		Browser, ClaudeCode bool
		Installs            map[string]installJob
	}
	read := func() state {
		var reply state
		code, data := call("GET", "/api/state", nil)
		if err := json.Unmarshal(data, &reply); code != 200 || err != nil {
			t.Fatalf("state: %d %s", code, data)
		}
		return reply
	}
	until := func(what string, done func(state) bool) state {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if reply := read(); done(reply) {
				return reply
			}
		}
		t.Fatalf("%s: %+v", what, read())
		return state{}
	}

	if code, _ := call("POST", "/api/install/firefox", nil); code != 404 {
		t.Fatalf("unknown install: %d", code)
	}
	if code, data := call("POST", "/api/install/chromium", nil); code != 202 {
		t.Fatalf("install: %d %s", code, data)
	}
	until("progress", func(s state) bool {
		return s.Installs["chromium"].Text == "Downloading Chromium… 50%" && s.Installs["chromium"].Running
	})
	if code, _ := call("POST", "/api/install/chromium", nil); code != 202 {
		t.Fatalf("a second click joins the running install: %d", code)
	}
	release <- struct{}{}
	reply := until("error", func(s state) bool { return !s.Installs["chromium"].Running })
	if reply.Installs["chromium"].Error != "storage.googleapis.com answered 503" || reply.Browser {
		t.Fatalf("failed install: %+v", reply)
	}

	fail = false
	call("POST", "/api/install/chromium", nil)
	release <- struct{}{}
	reply = until("installed", func(s state) bool { return !s.Installs["chromium"].Running })
	if reply.Installs["chromium"].Error != "" || !reply.Browser || !server.hasBrowser() {
		t.Fatalf("Chromium installed: %+v", reply)
	}
	if code, data := call("POST", "/api/install/chromium", nil); code != 200 || !strings.Contains(string(data), `"installed":true`) {
		t.Fatalf("already installed: %d %s", code, data)
	}

	call("POST", "/api/install/claude-code", nil)
	reply = until("Claude Code", func(s state) bool { return s.ClaudeCode })
	if reply.Installs["claude-code"].Error != "" {
		t.Fatalf("Claude Code installed: %+v", reply)
	}
}

// What Install Chromium and Install Claude Code leave in the data folder is
// what the slim server then reports and uses. (Only what existed before, so
// this test fails on the code without them.)
func TestDataFolderInstallsAreFound(t *testing.T) {
	t.Setenv("ONELOOP_BROWSER", "none")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if runtime.GOOS+"/"+runtime.GOARCH != "linux/amd64" {
		t.Skip("the test's Chromium folder is Linux x64's")
	}
	_, _, call := testServer(t)
	if code, data := call("GET", "/api/state", nil); code != 200 || !strings.Contains(string(data), `"browser":false`) || !strings.Contains(string(data), `"claudeCode":false`) {
		t.Fatalf("before: %d %s", code, data)
	}
	dir := filepath.Dir(browser.BrowserProfileOverride) // the server's data folder
	chrome := filepath.Join(dir, "chromium", "150.0.1.2", "chrome-linux64", "chrome")
	claude := filepath.Join(dir, "claude-code", "claude")
	for _, path := range []string{chrome, claude} {
		os.MkdirAll(filepath.Dir(path), 0755)
		os.WriteFile(path, []byte("#!/bin/sh\n"), 0755)
	}
	os.WriteFile(filepath.Join(dir, "chromium", "checked"), nil, 0600)
	if code, data := call("GET", "/api/state", nil); code != 200 || !strings.Contains(string(data), `"browser":true`) || !strings.Contains(string(data), `"claudeCode":true`) {
		t.Fatalf("after: %d %s", code, data)
	}
}
