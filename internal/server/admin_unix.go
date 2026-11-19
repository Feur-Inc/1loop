// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package server

import (
	"os"
	"syscall"
)

// ownedByMe: the file belongs to the user this server runs as.
func ownedByMe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
