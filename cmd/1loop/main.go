// The 1loop desktop program.
package main

import (
	"1loop/internal/platform"
	"1loop/internal/ui"
)

func main() {
	platform.SandboxMain() // when started as the sandbox of a command
	ui.Main()
}
