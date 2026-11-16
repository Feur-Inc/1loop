// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package softgl

import "errors"

// Load is Windows': elsewhere the system's Mesa draws on the CPU.
func Load(string) error { return errors.New("only Windows loads 1loop's Mesa") }
