// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// The XDG desktop portal over D-Bus: file choosers (filepicker.go) and the
// Flatpak's notifications (notify.go). The browser client has none
// (portal_js.go).

import (
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"github.com/rymdport/portal"
	"github.com/rymdport/portal/filechooser"
	"github.com/rymdport/portal/notification"

	"1loop/internal/platform"
)

// portalHandle names the parent window so the portal can keep its dialog
// on top of it. Wayland needs an exported xdg_foreign handle, which GLFW
// does not offer: the dialog is then simply centred by the compositor.
func portalHandle(window fyne.Window) string {
	handle := ""
	if native, ok := window.(driver.NativeWindow); ok {
		native.RunNative(func(context any) {
			if x, ok := context.(driver.X11WindowContext); ok && x.WindowHandle != 0 {
				handle = portal.FormatX11WindowHandle(x.WindowHandle)
			}
		})
	}
	return handle
}

// portalPick asks the portal's file chooser; false when there is no portal.
func portalPick(handle string, pickRequestValue pickRequest) ([]string, bool) {
	options := &filechooser.OpenFileOptions{Directory: pickRequestValue.folder, Multiple: pickRequestValue.multiple, CurrentFolder: pickRequestValue.start}
	if pickRequestValue.folder {
		options.AcceptLabel = "Open"
	}
	if len(pickRequestValue.extensions) > 0 {
		var rules []filechooser.Rule
		for _, ext := range pickRequestValue.extensions {
			rules = append(rules,
				filechooser.Rule{Type: filechooser.GlobPattern, Pattern: "*" + strings.ToLower(ext)},
				filechooser.Rule{Type: filechooser.GlobPattern, Pattern: "*" + strings.ToUpper(ext)})
		}
		filter := &filechooser.Filter{Name: pickRequestValue.filterName, Rules: rules}
		options.Filters, options.CurrentFilter = []*filechooser.Filter{filter}, filter
	}
	uris, err := filechooser.OpenFile(handle, pickRequestValue.title, options)
	if err != nil {
		return nil, false
	}
	paths := make([]string, 0, len(uris))
	for _, uri := range uris {
		paths = append(paths, strings.TrimPrefix(uri, "file://"))
	}
	return paths, true
}

// portalNotification numbers the portal's notifications: one id each, so a
// new one does not replace the last.
var portalNotification atomic.Uint64

// portalNotify shows a notification through the portal, with the
// application's own icon.
func portalNotify(title, text string) {
	if err := notification.Add(uint(portalNotification.Add(1)), notification.Content{Title: title, Body: text}); err != nil {
		platform.Devf("app", "notification portal: %v", err)
	}
}
