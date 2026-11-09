// SPDX-License-Identifier: MPL-2.0

package terminal

import (
	"context"
	"errors"
)

// NewTerminalProcess: the browser client has no shell of its own; its
// terminals are the server's.
func NewTerminalProcess(context.Context, string, ...NativeHost) (Process, error) {
	return nil, errors.New("Terminals run on the server in the browser")
}
