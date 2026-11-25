// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A lent browser browses the web for the server: what would reach this
// computer's files never gets to it.
func TestLendRefusal(t *testing.T) {
	for command, refused := range map[string]bool{
		`{"id":1,"method":"Page.navigate","params":{"url":"https://example.com"}}`:                      false,
		`{"id":1,"method":"Target.createTarget","params":{"url":"about:blank"}}`:                        false,
		`{"id":1,"method":"Runtime.evaluate","params":{"expression":"location='file:///etc/passwd'"}}`:  false,
		`{"id":1,"method":"Browser.setDownloadBehavior","params":{"behavior":"deny"}}`:                  false,
		`{"id":1,"method":"Page.navigate","params":{"url":"file:///etc/passwd"}}`:                       true,
		`{"id":1,"method":"Target.createTarget","params":{"url":"chrome://settings"}}`:                  true,
		`{"id":1,"method":"Network.loadNetworkResource","params":{"url":"file:///home"}}`:               true,
		`{"id":1,"method":"Browser.setDownloadBehavior","params":{"behavior":"allow"}}`:                 true,
		`{"id":1,"method":"Page.setDownloadBehavior","params":{"behavior":"allow","downloadPath":"/"}}`: true,
		`{"id":1,"method":"DOM.setFileInputFiles","params":{"files":["/etc/passwd"]}}`:                  true,
		`{"id":1,"method":"Input.dispatchDragEvent","params":{"type":"drop","data":{"files":["/x"]}}}`:  true,
		`{"id":1,"method":"Input.dispatchDragEvent","params":{"type":"drop","data":{"items":[]}}}`:      false,
		`{"id":1,"method":"Browser.grantPermissions","params":{"permissions":["clipboardReadWrite"]}}`:  true,
		`not json`: true,
	} {
		if got := lendRefusal(json.RawMessage(command)) != ""; got != refused {
			t.Errorf("%s: refused %v, want %v", command, got, refused)
		}
	}
}

func TestNamesProfile(t *testing.T) {
	for cmdline, want := range map[string]bool{
		"chrome\x00--headless=new\x00--user-data-dir=/p/lent\x00about:blank\x00":        true,
		"/snap/chromium/x/chrome --headless=new --user-data-dir=/p/lent --no-first-run": true, // snap's, in one string
		"chrome --user-data-dir=/p/lent":                                                true,
		"chrome --user-data-dir=/p/lent-other --x":                                      false,
		"chrome --user-data-dir=/p/lent2\x00":                                           false,
		"chrome --user-data-dir=/p/lent-other --user-data-dir=/p/lent":                  true,
		"sleep\x0030\x00": false,
	} {
		if got := namesProfile(cmdline, "/p/lent"); got != want {
			t.Errorf("%q: %v, want %v", cmdline, got, want)
		}
	}
}

// A lent browser that outlived its 1loop holds the profile's lock: the
// next lending stops it, but never a process that is not that browser.
func TestLentOrphan(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux's")
	}
	dir := t.TempDir()
	start := func(args ...string) *exec.Cmd {
		command := exec.Command("sh", append([]string{"-c", "sleep 30; :", "sh"}, args...)...) // sh stays: its arguments are on its command line
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { command.Process.Kill(); command.Wait() })
		for i := 0; i < 100; i++ { // until sh runs: before, /proc shows this test's command line
			cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", command.Process.Pid))
			if strings.Contains(string(cmdline), "--user-data-dir=") {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		return command
	}
	lock := func(pid int) {
		os.Remove(filepath.Join(dir, "SingletonLock"))
		if err := os.Symlink(fmt.Sprintf("host-%d", pid), filepath.Join(dir, "SingletonLock")); err != nil {
			t.Fatal(err)
		}
	}
	other := start("--user-data-dir=" + dir + "-other")
	lock(other.Process.Pid)
	lentOrphan(dir)
	if other.Process.Signal(syscall.Signal(0)) != nil {
		t.Fatal("a process that is not the lent browser was stopped")
	}
	orphan := start("--user-data-dir=" + dir)
	lock(orphan.Process.Pid)
	done := make(chan error, 1)
	go func() { done <- orphan.Wait() }()
	lentOrphan(dir)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the orphan lent browser still runs")
	}
}
