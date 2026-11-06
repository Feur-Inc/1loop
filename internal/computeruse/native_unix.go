// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package computeruse

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"1loop/assets"
	"1loop/internal/platform"
)

// computerPython runs the portal helper: the system's own Python, where the
// distribution installs python3-gi and its GStreamer bindings (a virtualenv
// or pyenv python3 found first on PATH usually lacks them). Packages that
// bring their own (Nix) or reach the host's (Flatpak) set
// ONELOOP_COMPUTER_PYTHON.
func computerPython() string {
	if python := os.Getenv("ONELOOP_COMPUTER_PYTHON"); python != "" {
		return python
	}
	return "/usr/bin/python3"
}

func startNativeComputer(ctx context.Context) (*computerProcess, error) {
	return startComputerProcess(ctx, computerPython(), "-u", "-c", assets.ComputerUsePortal)
}
func ComputerGuideText() string { return computerGuide }

// portalCheck imports what the portal helper imports and prints, one per
// line, what this Python lacks.
const portalCheck = `
import importlib
missing = []
def need(module, name):
    try:
        importlib.import_module(module)
        return True
    except Exception:
        missing.append(name)
        return False
need("dbus", "dbus (dbus-python)")
need("PIL", "PIL (Pillow)")
if need("gi", "gi (PyGObject)"):
    import gi
    gst = True
    for module, version, name in (("Gst", "1.0", "GStreamer"), ("GstVideo", "1.0", "GStreamer base plugins"), ("Gdk", "3.0", "GTK 3")):
        try:
            gi.require_version(module, version)
            importlib.import_module("gi.repository." + module)
        except Exception:
            missing.append("the %s %s typelib (%s)" % (module, version, name))
            gst = gst and module != "Gst"
    if gst:
        from gi.repository import Gst
        Gst.init(None)
        if Gst.ElementFactory.find("pipewiresrc") is None:
            missing.append("pipewiresrc (GStreamer's PipeWire plugin)")
print("\n".join(missing))
`

// PortalHelperReport says whether start can open sharing: whether the Python
// that runs the portal helper has what the helper imports. doctor reports it
// before start, which would otherwise fail with only the helper's exit.
func PortalHelperReport(ctx context.Context) string {
	python := computerPython()
	command := exec.CommandContext(ctx, python, "-c", portalCheck)
	platform.PrepareComputerProcess(command)
	output, err := command.Output()
	if err != nil {
		reason := err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if text := strings.TrimSpace(string(exit.Stderr)); text != "" {
				reason = text[strings.LastIndexByte(text, '\n')+1:]
			}
		}
		return "unavailable, start would fail: " + python + " doesn't run: " + reason
	}
	missing := strings.Split(string(bytes.TrimSpace(output)), "\n")
	if missing[0] == "" {
		return "ready: start asks the user; " + python + " has what the sharing helper needs"
	}
	return "unavailable, start would fail: " + python + " lacks " + strings.Join(missing, ", ") + ". Install what's missing from the distribution's packages, then call start"
}
