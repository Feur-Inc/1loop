// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// A server chat's attachments come from this computer's file chooser; the
// browser client has its own (web_attach_js.go).

import "os"

func (remote *remoteDesktop) pickAttachments(images bool) {
	home, _ := os.UserHomeDir()
	request := pickRequest{title: "Attach files", start: home, multiple: true}
	if images {
		request = pickRequest{title: "Attach images", start: home, multiple: true, filterName: "Images",
			extensions: []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"}}
	}
	remote.parent.pick(request, remote.attachPaths)
}

// pasteBrowserImage is the browser's clipboard (web_attach_js.go): none here.
func (ui *desktopUI) pasteBrowserImage() bool { return false }
