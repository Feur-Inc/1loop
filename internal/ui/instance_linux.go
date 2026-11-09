// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"os"
)

// instanceAddress is an abstract socket, per user: the kernel frees it
// when 1loop ends, however it ends. The Flatpak shares the network
// namespace, so it and a native 1loop see the same one.
func instanceAddress() string {
	return fmt.Sprintf("@io.1loop.desktop-%d", os.Getuid())
}
