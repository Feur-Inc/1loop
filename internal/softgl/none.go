// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package softgl

import "embed"

// Elsewhere 1loop uses the system's Mesa (internal/ui, backend_linux.go).
var licenses embed.FS

func files() map[string][]byte { return nil }
