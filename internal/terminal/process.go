// Package terminal runs the panel's shells in a pseudo-terminal (a PTY on
// Unix, a ConPTY on Windows).
package terminal

import "io"

// Both the panel and the server own a real pseudo-terminal. Platform files
// provide Unix PTYs or Windows ConPTY without changing tool/session semantics.
type Process interface {
	io.ReadWriteCloser
	Resize(columns, rows int) error
	Wait() int
}
