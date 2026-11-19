// SPDX-License-Identifier: MPL-2.0

package ui

// One 1loop per user: a second launch brings the running one's window to
// the front and exits, instead of a second window, tray icon and state
// writer. Started on a folder (-workspace), it has that one opened there.

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"1loop/internal/platform"
)

// claimInstance makes this process the user's 1loop and returns the
// listener later launches reach it on (nil when that cannot be had). When
// a 1loop already runs, it shows that one's window, with folder open in it
// when one was asked for, and exits.
func claimInstance(folder string) net.Listener {
	address := instanceAddress()
	if address == "" {
		return nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		if l, err := net.Listen("unix", address); err == nil {
			return l
		}
		if folder != "" && askToOpen(address, folder) {
			platform.Devf("app", "1loop already runs: %s was opened in its window", folder)
			os.Exit(0)
		}
		// No folder, or a 1loop from before "open" (#511): its window still
		// comes to the front.
		if askToShow(address) {
			platform.Devf("app", "1loop already runs: its window was brought to the front")
			os.Exit(0)
		}
		// Taken but nobody answers: a socket file left by a 1loop that
		// was killed (an abstract address never stays behind).
		if address[0] != '@' {
			os.Remove(address)
		}
	}
	// Still not ours: run anyway rather than not at all.
	platform.Devf("app", "could not claim %s: running without the one-instance check", address)
	return nil
}

// askToShow tells the running 1loop to show its window.
func askToShow(address string) bool { return askInstance(address, "show") }

// askToOpen tells the running 1loop to open a folder (adding it to its
// projects if needed) and show its window. A 1loop from before it answers
// nothing, as to any request it does not know.
func askToOpen(address, folder string) bool {
	if strings.ContainsAny(folder, "\n\r") {
		return false
	}
	return askInstance(address, "open "+folder)
}

func askInstance(address, request string) bool {
	conn, err := net.DialTimeout("unix", address, time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte(request + "\n")); err != nil {
		return false
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	return err == nil && reply == "ok\n"
}

// serveInstance answers later launches: each one shows the window, and
// opens the folder it was started on, if any.
func serveInstance(l net.Listener, show func(), open func(folder string)) {
	if l == nil {
		return
	}
	go func() {
		for {
			conn, err := l.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			} else if err != nil {
				continue
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(2 * time.Second))
				request, err := bufio.NewReader(conn).ReadString('\n')
				request = strings.TrimSuffix(request, "\n")
				switch {
				case err != nil:
					return
				case request == "show":
					platform.Devf("app", "another launch: window brought to the front")
					show()
				case strings.HasPrefix(request, "open ") && filepath.IsAbs(strings.TrimPrefix(request, "open ")):
					folder := strings.TrimPrefix(request, "open ")
					platform.Devf("app", "another launch on %s: opened in the window", folder)
					open(folder)
				default:
					return
				}
				conn.Write([]byte("ok\n"))
			}()
		}
	}()
}
