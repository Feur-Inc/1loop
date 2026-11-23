// SPDX-License-Identifier: MPL-2.0

package update

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// uninstallKey is where the setup (cmd/1loop-setup) lists 1loop
// for "Installed apps".
const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\1loop`

// RecordInstalled writes the running version in "Installed apps" when it is
// the installed copy that runs: its own update replaced the exe, not what
// the setup wrote there.
func RecordInstalled() {
	exe, err := running()
	if err != nil {
		return
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return // not installed by the setup
	}
	defer key.Close()
	dir, _, err := key.GetStringValue("InstallLocation")
	if err != nil || !strings.EqualFold(filepath.Clean(dir), filepath.Dir(exe)) {
		return
	}
	if listed, _, _ := key.GetStringValue("DisplayVersion"); listed != Version {
		key.SetStringValue("DisplayVersion", Version)
	}
}
