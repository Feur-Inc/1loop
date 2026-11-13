// SPDX-License-Identifier: MPL-2.0

//go:build !windows || !mesa

package softgl

// Builds other than the Windows release carry no Mesa.
func files() map[string][]byte { return nil }
