// SPDX-License-Identifier: MPL-2.0

package ui

// Settings in the browser client (issue #267): the page has no computer of
// its own, so each section is the server's, through its API. The desktop's
// own pages would read files that are not there ("open extensions.json: not
// implemented on js") or reach sites a page may not (the release page, for
// updates); Connections, Extensions and Connectors are the pages the desktop
// shows for a server under Settings → Servers (settings_servers.go).

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	registry "1loop/internal/settings"
)

// SSH machines and the phone remote are this computer's: a page has neither.
func settingsSectionShown(name string) bool {
	return name != "SSH machines" && name != "Phone"
}

func (ui *desktopUI) browserSettings(section string) fyne.CanvasObject {
	remote := ui.server
	if remote == nil {
		return container.NewVBox(ui.prefs.pageTitle(section, "", nil),
			card(settingRow("Not connected", "Settings are the server's: they show once this page is connected to it.", nil)))
	}
	view := ui.prefs
	title := func(section, back string) serverHead {
		return func(trailing fyne.CanvasObject) fyne.CanvasObject { return view.pageTitle(section, back, trailing) }
	}
	switch section {
	case "Agent & approvals":
		return remote.instructionSettings()
	case "Models & accounts":
		return container.NewVBox(view.mark("models.connections", remote.connectionSettings(title(section, ""))),
			sectionTitle("Chat names"), card(view.mark(registry.KeyNamingModel, remote.namingSetting())))
	case "Extensions":
		return ui.extensionSettings(serverExtensions{remote, section}, title(section, "Tools & extensions")(nil))
	case "Connectors":
		return ui.connectorSettings(serverGitHub{remote, section}, title(section, "Tools & extensions")(nil))
	case "Tools & extensions":
		return nil // this page's own
	case "Updates":
		return remote.updateSettings(title(section, ""))
	default:
		return remote.generalSettings()
	}
}
