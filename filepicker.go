//go:build !server

package main

// Folder and file choosers use the desktop's own dialog: the XDG desktop
// portal over D-Bus (what GNOME and KDE apps use), then zenity or kdialog,
// and Fyne's built-in dialog only when none of them is available.

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/storage"
	"github.com/rymdport/portal"
	"github.com/rymdport/portal/filechooser"
)

type pickRequest struct {
	title      string
	start      string   // folder the chooser opens in
	folder     bool     // choose a directory instead of files
	multiple   bool     // allow several files
	filterName string   // label of the file filter, e.g. "Images"
	extensions []string // accepted extensions with their dot, e.g. ".png"
}

var errNoNativePicker = errors.New("no native file chooser")

// pick opens the chooser without blocking the interface and calls done on
// the UI thread with the chosen paths (none when the user cancelled).
func (ui *desktopUI) pick(pickRequestValue pickRequest, done func([]string)) {
	handle := portalHandle(ui.window)
	go func() {
		paths, err := nativePick(handle, pickRequestValue)
		fyne.Do(func() {
			switch {
			case errors.Is(err, errNoNativePicker):
				ui.fynePick(pickRequestValue, done)
			case err != nil:
				ui.notice(pickRequestValue.title, err.Error())
			case len(paths) > 0:
				done(paths)
			}
		})
	}()
}

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

func nativePick(handle string, pickRequestValue pickRequest) ([]string, error) {
	if runtime.GOOS == "windows" {
		return nil, errNoNativePicker
	}
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
	if uris, err := filechooser.OpenFile(handle, pickRequestValue.title, options); err == nil {
		paths := make([]string, 0, len(uris))
		for _, uri := range uris {
			paths = append(paths, strings.TrimPrefix(uri, "file://"))
		}
		return paths, nil
	}
	// No portal on this desktop: try the classic helpers.
	if _, err := exec.LookPath("zenity"); err == nil {
		return runChooser(zenityArgs(pickRequestValue))
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		return runChooser(kdialogArgs(pickRequestValue))
	}
	return nil, errNoNativePicker
}

func zenityArgs(pickRequestValue pickRequest) []string {
	args := []string{"zenity", "--file-selection", "--title=" + pickRequestValue.title, "--separator=\n"}
	if pickRequestValue.start != "" {
		args = append(args, "--filename="+strings.TrimSuffix(pickRequestValue.start, "/")+"/")
	}
	if pickRequestValue.folder {
		args = append(args, "--directory")
	}
	if pickRequestValue.multiple {
		args = append(args, "--multiple")
	}
	if len(pickRequestValue.extensions) > 0 {
		args = append(args, "--file-filter="+pickRequestValue.filterName+" | "+globs(pickRequestValue.extensions))
	}
	return args
}

func kdialogArgs(pickRequestValue pickRequest) []string {
	start := pickRequestValue.start
	if start == "" {
		start = "."
	}
	if pickRequestValue.folder {
		return []string{"kdialog", "--title", pickRequestValue.title, "--getexistingdirectory", start}
	}
	args := []string{"kdialog", "--title", pickRequestValue.title, "--getopenfilename", start}
	if len(pickRequestValue.extensions) > 0 {
		args = append(args, globs(pickRequestValue.extensions)+"|"+pickRequestValue.filterName)
	}
	if pickRequestValue.multiple {
		args = append(args, "--multiple", "--separate-output")
	}
	return args
}

func globs(extensions []string) string {
	patterns := make([]string, len(extensions))
	for i, ext := range extensions {
		patterns[i] = "*" + ext
	}
	return strings.Join(patterns, " ")
}

// runChooser runs zenity or kdialog; both exit with status 1 on cancel.
func runChooser(args []string) ([]string, error) {
	out, err := exec.Command(args[0], args[1:]...).Output()
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", args[0], err)
	}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// fynePick is the last resort, drawn by Fyne inside the window.
func (ui *desktopUI) fynePick(pickRequestValue pickRequest, done func([]string)) {
	var start fyne.ListableURI
	if pickRequestValue.start != "" {
		start, _ = storage.ListerForURI(storage.NewFileURI(pickRequestValue.start))
	}
	if pickRequestValue.folder {
		d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				ui.notice(pickRequestValue.title, err.Error())
			} else if uri != nil {
				done([]string{uri.Path()})
			}
		}, ui.window)
		d.SetLocation(start)
		d.Show()
		return
	}
	d := dialog.NewFileOpen(func(file fyne.URIReadCloser, err error) {
		if err != nil {
			ui.notice(pickRequestValue.title, err.Error())
			return
		}
		if file != nil {
			file.Close()
			done([]string{file.URI().Path()})
		}
	}, ui.window)
	d.SetLocation(start)
	if len(pickRequestValue.extensions) > 0 {
		d.SetFilter(storage.NewExtensionFileFilter(pickRequestValue.extensions))
	}
	d.Show()
}
