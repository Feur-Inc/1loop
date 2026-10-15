package main

import "io"

// Both the panel and the server own a real pseudo-terminal. Platform files
// provide Unix PTYs or Windows ConPTY without changing tool/session semantics.
type terminalProcess interface {
	io.ReadWriteCloser
	resize(columns, rows int) error
	wait() int
}
