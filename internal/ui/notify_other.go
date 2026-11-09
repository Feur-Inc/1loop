// SPDX-License-Identifier: MPL-2.0

//go:build !linux

package ui

func sendClickableNotification(title, body string, click func()) bool { return false }
