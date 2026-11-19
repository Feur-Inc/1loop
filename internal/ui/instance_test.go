// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"bufio"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestSecondLaunchShowsTheWindow(t *testing.T) {
	address := filepath.Join(t.TempDir(), "desktop.sock")
	if askToShow(address) {
		t.Fatal("nobody listens yet")
	}
	l, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	shown := make(chan bool, 1)
	serveInstance(l, func() { shown <- true }, func(string) { t.Error("nothing to open") })
	if !askToShow(address) {
		t.Fatal("the running instance did not answer")
	}
	if !<-shown {
		t.Fatal("the window was not shown")
	}
}

// A second launch on a folder has the running 1loop open it (#511): the
// folder was dropped and only the window shown.
func TestSecondLaunchOpensItsFolder(t *testing.T) {
	address := filepath.Join(t.TempDir(), "desktop.sock")
	l, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	opened := make(chan string, 1)
	serveInstance(l, func() { t.Error("shown without the folder") }, func(folder string) { opened <- folder })
	folder := filepath.Join(t.TempDir(), "project-b")
	if !askToOpen(address, folder) {
		t.Fatal("the running instance did not answer")
	}
	if got := <-opened; got != folder {
		t.Fatalf("opened %q, want %q", got, folder)
	}
	if askToOpen(address, "relative/path") || askToOpen(address, folder+"\nshow") {
		t.Error("a relative or multi-line folder was accepted")
	}
}

// A 1loop from before "open" answers nothing to it: the new launch then
// asks it to show its window, rather than taking its place.
func TestSecondLaunchFallsBackToShow(t *testing.T) {
	address := filepath.Join(t.TempDir(), "desktop.sock")
	l, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() { // the old protocol: "show" only
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			if request, _ := bufio.NewReader(conn).ReadString('\n'); request == "show\n" {
				conn.Write([]byte("ok\n"))
			}
			conn.Close()
		}
	}()
	if askToOpen(address, filepath.Join(t.TempDir(), "b")) {
		t.Fatal("an old 1loop said it opened the folder")
	}
	if !askToShow(address) {
		t.Fatal("an old 1loop no longer shows its window")
	}
}
