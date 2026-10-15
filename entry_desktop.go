//go:build !server

package main

// The desktop program's entry point (the headless server's is entry_server.go).

import (
	"flag"
	"log"
	"os"
)

const terminalScreenVisibility = "This chat must be visible; text output remains readable in background chats."

func main() {
	workspace := flag.String("workspace", ".", "Workspace")
	developer := flag.Bool("dev", false, "Write a detailed developer log (events, performance, stalls) to the terminal and to ~/.cache/1loop/dev.log")
	flag.Parse()
	devWanted = *developer || os.Getenv("ONELOOP_DEV") == "1"
	root, err := resolveWorkspace(*workspace)
	if err != nil {
		log.Fatal(err)
	}
	backend := &app{approvals: make(map[string]chan bool)}
	runDesktop(backend, root)
}
