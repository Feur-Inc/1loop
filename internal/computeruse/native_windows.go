package computeruse

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"1loop/assets"
)

func startNativeComputer(ctx context.Context) (*computerProcess, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "1loop")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "computer-use.ps1")
	if err = os.WriteFile(path, []byte(assets.ComputerUseWindows), 0600); err != nil {
		return nil, err
	}
	return startComputerProcess(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-ExecutionPolicy", "Bypass", "-File", path)
}
func ComputerGuideText() string {
	guide := strings.Replace(computerGuide, "1loop opens the desktop's screen-sharing and remote-control consent dialog.", "1loop opens its Windows consent dialog, where the user selects a monitor or the whole desktop and can remember the grant.", 1)
	guide = strings.ReplaceAll(guide, "Wayland accessibility bounds may be local to the application, so do not treat them as global screen coordinates.", "Windows UI Automation bounds are physical desktop pixels; subtract the shared screen origin to get input coordinates. Locked desktops, UAC secure desktops and higher-integrity applications cannot be controlled. Ask the user to unlock or handle these surfaces; never bypass them.")
	guide = strings.Replace(guide, "Coordinates are logical pixels", "Coordinates are physical pixels", 1)
	return guide
}
