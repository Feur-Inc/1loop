// The headless 1loop server: no display, reached from the phone over HTTPS.
package main

import (
	"1loop/internal/platform"
	"1loop/internal/server"
)

func main() {
	platform.SandboxMain() // when started as the sandbox of a command
	server.Main()
}
