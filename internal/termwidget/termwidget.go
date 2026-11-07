// SPDX-License-Identifier: MPL-2.0

//go:build !js

// Package termwidget is the terminal widget of the side panel: Fyne's
// terminal everywhere but in the browser client (termwidget_js.go), where
// its PTY code does not build.
package termwidget

import fyneterm "github.com/fyne-io/terminal"

type (
	Terminal = fyneterm.Terminal
	Config   = fyneterm.Config
)

// New makes a terminal widget.
func New() *Terminal { return fyneterm.New() }
