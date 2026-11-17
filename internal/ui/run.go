// SPDX-License-Identifier: MPL-2.0

// Package ui is the 1loop desktop: its Fyne window, the chats, the side
// panel, the settings, and the phone remote it serves.
package ui

// The desktop program's entry point (the headless server's is internal/server/run.go).

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"1loop/internal/agent"
	"1loop/internal/crash"
	"1loop/internal/platform"
)

// Main runs the desktop program.
func Main() {
	workspace := flag.String("workspace", ".", "Workspace")
	developer := flag.Bool("dev", false, "Write a detailed developer log (events, performance, stalls) to the terminal and to ~/.cache/1loop/dev.log")
	flag.Parse()
	tuneMemory()
	platform.DevWanted = *developer || os.Getenv("ONELOOP_DEV") == "1"
	root, err := agent.ResolveWorkspace(*workspace)
	if err != nil {
		log.Fatal(err)
	}
	agent.TerminalScreenVisibility = "This chat must be visible; text output remains readable in background chats."
	// Killed or interrupted (Ctrl+C), 1loop still ends the background
	// commands it started: they run in their own process groups.
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		agent.StopEveryTask()
		crash.Clean() // ended on purpose (logout, Ctrl+C): no crash
		os.Exit(130)
	}()
	backend := &agent.App{Approvals: make(map[string]chan bool)}
	runDesktop(backend, root)
}

// tuneMemory paces the garbage collector for a desktop app that stays open
// all day (issue #476): a collection when the heap grows by half rather than
// doubles (GOGC=50). No memory limit: one set below the live heap (a very
// long chat open) made the collector run without pause and the window crawl
// on the test VM. GOGC in the environment still wins.
func tuneMemory() {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(50)
	}
}
