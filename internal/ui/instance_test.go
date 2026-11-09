// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"net"
	"path/filepath"
	"testing"
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
	serveInstance(l, func() { shown <- true })
	if !askToShow(address) {
		t.Fatal("the running instance did not answer")
	}
	if !<-shown {
		t.Fatal("the window was not shown")
	}
}
