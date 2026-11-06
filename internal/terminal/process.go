// SPDX-License-Identifier: MPL-2.0

// Package terminal runs the panel's shells in a pseudo-terminal (a PTY on
// Unix, a ConPTY on Windows).
package terminal

import (
	"fmt"
	"io"
	"os"
)

// Both the panel and the server own a real pseudo-terminal. Platform files
// provide Unix PTYs or Windows ConPTY without changing tool/session semantics.
type Process interface {
	io.ReadWriteCloser
	Resize(columns, rows int) error
	Wait() int
}

// folderProblem says plainly that a terminal's folder is missing: starting
// the shell there fails with an error naming the shell, as if it were the
// one missing.
func folderProblem(dir string) error {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		return fmt.Errorf("The folder %s doesn't exist", dir)
	case err != nil:
		return fmt.Errorf("Can't open the folder %s: %w", dir, err)
	case !info.IsDir():
		return fmt.Errorf("%s is a file, not a folder", dir)
	}
	return nil
}
