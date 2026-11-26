// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

// The XDG desktop portal's file choosers over D-Bus (filepicker.go). The
// browser client has none (portal_js.go).

import (
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
	"github.com/rymdport/portal"
	"github.com/rymdport/portal/filechooser"
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
	if pickRequestValue.save {
		return portalSave(handle, pickRequestValue)
	}
	options := &filechooser.OpenFileOptions{Directory: pickRequestValue.folder, Multiple: pickRequestValue.multiple, CurrentFolder: pickRequestValue.start}
	if pickRequestValue.folder {
		options.AcceptLabel = "Open"
	}
	if filter := portalFilter(pickRequestValue); filter != nil {
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

// portalFilter is the request's file filter; nil without one.
func portalFilter(pickRequestValue pickRequest) *filechooser.Filter {
	if len(pickRequestValue.extensions) == 0 {
		return nil
	}
	var rules []filechooser.Rule
	for _, ext := range pickRequestValue.extensions {
		rules = append(rules,
			filechooser.Rule{Type: filechooser.GlobPattern, Pattern: "*" + strings.ToLower(ext)},
			filechooser.Rule{Type: filechooser.GlobPattern, Pattern: "*" + strings.ToUpper(ext)})
	}
	return &filechooser.Filter{Name: pickRequestValue.filterName, Rules: rules}
}

// portalSave asks the portal where to save a new file.
func portalSave(handle string, pickRequestValue pickRequest) ([]string, bool) {
	options := &filechooser.SaveFileOptions{CurrentFolder: pickRequestValue.start, CurrentName: pickRequestValue.name, AcceptLabel: "Save"}
	if filter := portalFilter(pickRequestValue); filter != nil {
		options.Filters, options.CurrentFilter = []*filechooser.Filter{filter}, filter
	}
	uris, err := filechooser.SaveFile(handle, pickRequestValue.title, options)
	if err != nil {
		return nil, false
	}
	paths := make([]string, 0, len(uris))
	for _, uri := range uris {
		paths = append(paths, fileURIPath(uri))
	}
	return paths, true
}

// portalNotification numbers the portal's notifications: one id each, so a
// new one does not replace the last.
var portalNotification atomic.Uint64
