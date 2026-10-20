// Package ui is the 1loop desktop: its Fyne window, the chats, the side
// panel, the settings, and the phone remote it serves.
package ui

// The desktop program's entry point (the headless server's is internal/server/run.go).

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"1loop/internal/agent"
	"1loop/internal/platform"
)

// Main runs the desktop program.
func Main() {
	workspace := flag.String("workspace", ".", "Workspace")
	developer := flag.Bool("dev", false, "Write a detailed developer log (events, performance, stalls) to the terminal and to ~/.cache/1loop/dev.log")
	flag.Parse()
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
		os.Exit(130)
	}()
	backend := &agent.App{Approvals: make(map[string]chan bool)}
	runDesktop(backend, root)
}
