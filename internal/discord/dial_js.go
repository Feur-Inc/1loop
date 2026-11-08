// SPDX-License-Identifier: MPL-2.0

package discord

import (
	"errors"
	"io"
)

// dial has nothing to reach from a browser.
func dial() (io.ReadWriteCloser, error) {
	return nil, errors.New("no Discord in the browser")
}
