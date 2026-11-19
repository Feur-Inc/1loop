// SPDX-License-Identifier: MPL-2.0

package server

import "os"

// ownedByMe: on Windows the temporary folder is the user's own.
func ownedByMe(os.FileInfo) bool { return true }
