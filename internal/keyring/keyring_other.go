// SPDX-License-Identifier: MPL-2.0

//go:build !(linux && !android) && !windows && !darwin

package keyring

func system() Store { return nil }
