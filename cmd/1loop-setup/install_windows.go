// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// uninstallKey lists 1loop in "Installed apps"; the app keeps its version
// right after its own updates (internal/update/installed_windows.go).
const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\1loop`

const appName, uninstallerName = "1loop.exe", "uninstall.exe"

// installedDir is where 1loop is installed, "" when it is not.
func installedDir() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	dir, _, _ := key.GetStringValue("InstallLocation")
	return dir
}

// defaultDir is the folder of the installed copy, or the user's programs.
func defaultDir() string {
	if dir := installedDir(); dir != "" {
		return dir
	}
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		local = os.Getenv("LOCALAPPDATA")
	}
	return filepath.Join(local, "Programs", "1loop")
}

// ownDir gives 1loop a folder of its own in the one chosen.
func ownDir(dir string) string {
	dir = filepath.Clean(dir)
	if strings.EqualFold(filepath.Base(dir), "1loop") {
		return dir
	}
	return filepath.Join(dir, "1loop")
}

// busy says the app is running: Windows refuses to write a running exe.
func busy(path string) bool {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_USER_MAPPED_FILE) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	file.Close()
	return false
}

func shortcutPath(folder *windows.KNOWNFOLDERID) string {
	dir, err := windows.KnownFolderPath(folder, 0)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop.lnk")
}

// install writes the app and the uninstaller in dir, and lists them.
func install(dir string, menu, desktop bool, progress func(float64)) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	source, err := os.Open(self)
	if err != nil {
		return err
	}
	defer source.Close()
	app, err := readPayload(source)
	if err != nil {
		return err
	}
	if app == nil {
		return errors.New("this setup carries no app: download it again")
	}
	target := filepath.Join(dir, appName)
	if busy(target) {
		return errRunning
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("Cannot make %s: %w", dir, err)
	}
	fresh := target + ".new"
	if err := write(fresh, func(to io.Writer) error {
		return app.extract(source, to, func(done float64) { progress(done * .97) })
	}); err != nil {
		return err
	}
	if err := os.Rename(fresh, target); err != nil {
		os.Remove(fresh)
		if busy(target) { // started meanwhile
			return errRunning
		}
		return err
	}
	os.Remove(target + ".old") // the updater's copy of the version before
	uninstaller := filepath.Join(dir, uninstallerName)
	if err := write(uninstaller, func(to io.Writer) error {
		_, err := io.Copy(to, io.NewSectionReader(source, 0, app.program))
		return err
	}); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("Cannot list 1loop in Installed apps: %w", err)
	}
	defer key.Close()
	for name, value := range map[string]string{
		"DisplayName": "1loop", "DisplayVersion": version, "DisplayIcon": target, "Publisher": "Feur-Inc",
		"URLInfoAbout": "https://github.com/Feur-Inc/1loop", "InstallLocation": dir,
		"UninstallString": `"` + uninstaller + `"`, "QuietUninstallString": `"` + uninstaller + `" /S`,
	} {
		if err := key.SetStringValue(name, value); err != nil {
			return fmt.Errorf("Cannot list 1loop in Installed apps: %w", err)
		}
	}
	key.SetDWordValue("NoModify", 1)
	key.SetDWordValue("NoRepair", 1)
	key.SetDWordValue("EstimatedSize", uint32((app.app+app.program)>>10))
	if menu {
		err = errors.Join(err, shortcut(shortcutPath(windows.FOLDERID_Programs), target))
	}
	if desktop {
		err = errors.Join(err, shortcut(shortcutPath(windows.FOLDERID_Desktop), target))
	}
	return err
}

// write makes a file whole, or not at all.
func write(path string, fill func(io.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("Cannot write %s: %w", path, err)
	}
	err = fill(file)
	if closed := file.Close(); err == nil {
		err = closed
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// uninstall removes what the installer, the updater and the software
// renderer wrote, from the uninstaller's own folder. Chats and settings
// (%AppData%\1loop) stay.
func uninstall() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(self)
	app := filepath.Join(dir, appName)
	if busy(app) {
		return errRunning
	}
	if err := os.Remove(app); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Cannot remove %s: %w", app, err)
	}
	os.Remove(app + ".old")
	os.Remove(app + ".new")
	if strings.EqualFold(filepath.Clean(installedDir()), filepath.Clean(dir)) {
		os.Remove(shortcutPath(windows.FOLDERID_Programs))
		os.Remove(shortcutPath(windows.FOLDERID_Desktop))
		if err := registry.DeleteKey(registry.CURRENT_USER, uninstallKey); err != nil {
			return fmt.Errorf("Cannot take 1loop out of Installed apps: %w", err)
		}
	}
	if local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0); err == nil {
		renderers, _ := filepath.Glob(filepath.Join(local, "1loop", "mesa-*"))
		for _, renderer := range renderers {
			os.RemoveAll(renderer)
		}
	}
	return nil
}

// leave removes the uninstaller itself and its folder, once it has ended:
// a running exe cannot be deleted. The folder stays if it holds anything else.
func leave() {
	self, err := os.Executable()
	if err != nil || !strings.EqualFold(filepath.Base(self), uninstallerName) {
		return
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return
	}
	command := exec.Command(filepath.Join(system, "cmd.exe"))
	command.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       fmt.Sprintf(`cmd.exe /d /c ping -n 3 127.0.0.1 >nul & del /f /q "%s" & rmdir "%s"`, self, filepath.Dir(self)),
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_BREAKAWAY_FROM_JOB,
	}
	command.Dir = system // not the folder to remove
	if command.Start() != nil {
		command = &exec.Cmd{Path: command.Path, Dir: system, SysProcAttr: &syscall.SysProcAttr{CmdLine: command.SysProcAttr.CmdLine, CreationFlags: windows.CREATE_NO_WINDOW}}
		command.Start() // in a job nothing leaves
	}
}

// launch starts the installed app, from the user's folder as a shortcut does.
func launch(dir string) {
	command := exec.Command(filepath.Join(dir, appName))
	command.Dir, _ = os.UserHomeDir()
	command.Start()
}
