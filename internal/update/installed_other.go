// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package update

// RecordInstalled has nothing to keep: only the Windows setup lists a version.
func RecordInstalled() {}
