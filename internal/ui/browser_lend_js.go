// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client lends no browser (browser_lend.go): a page cannot
// speak the DevTools protocol to one. Where the server has none, Settings
// says to install Chromium there or use the desktop app (server_install.go).

import (
	"context"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

func lendBrowser(context.Context, serverAddress, *remoteClient) {}

func (ui *desktopUI) buildLendBar() fyne.CanvasObject {
	ui.lendBar = container.NewStack()
	ui.lendBar.Hide()
	return ui.lendBar
}

func (ui *desktopUI) lendRows() fyne.CanvasObject { return nil }
