// SPDX-License-Identifier: MPL-2.0

package ui

// Theme files (issue #464): the themes of ~/.config/1loop/themes, read
// again as soon as one is added or saved, their accents in place of the
// presets, their corner roundness, and a theme per project.

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynedriver "fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/fsnotify/fsnotify"

	"1loop/internal/palette"
	"1loop/internal/platform"
	registry "1loop/internal/settings"
)

var (
	themeFiles      []palette.ThemeFile  // the theme files that read, by name
	themeFileErrors []palette.ThemeError // those that did not, shown in Appearance
	themesDir                            = palette.ThemesDir()
	// roundness scales the corners of cards, buttons, fields and bubbles:
	// the active theme's, 1 for 1loop's own.
	roundness float32 = 1
)

// rounded is a corner radius at the theme's roundness.
func rounded(radius float32) float32 { return radius * roundness }

// themeFile is the theme file a setting names, if it is one that reads.
func themeFile(setting string) (palette.ThemeFile, bool) {
	if !strings.HasPrefix(setting, palette.ThemeFilePrefix) {
		return palette.ThemeFile{}, false
	}
	for _, theme := range themeFiles {
		if theme.Setting() == setting {
			return theme, true
		}
	}
	return palette.ThemeFile{}, false
}

// loadThemeFiles reads the folder again, and says whether anything changed.
func loadThemeFiles() {
	themeFiles, themeFileErrors = palette.LoadThemes(themesDir)
	for _, failed := range themeFileErrors {
		platform.Devf("ui", "theme file %s", failed.Error())
	}
}

// themesStamp sums up the folder: names, sizes and times of its files, and
// of the files inside theme folders.
func themesStamp() string {
	var b strings.Builder
	filepath.WalkDir(themesDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if rel, _ := filepath.Rel(themesDir, path); strings.Count(rel, string(filepath.Separator)) > 2 {
			return filepath.SkipDir
		}
		if info, err := entry.Info(); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return b.String()
}

// watchThemes reads the theme files, then reads them again when a file of
// the folder is added, saved or removed: the picker lists the new file, and
// the theme in use is painted again. The folder is watched by notification
// (inotify and the like); without it, or while the folder does not exist,
// it is looked at every few seconds (it was every second, forever: #474).
func (ui *desktopUI) watchThemes() {
	loadThemeFiles()
	stamp := themesStamp()
	go func() {
		var events chan fsnotify.Event
		watcher, err := fsnotify.NewWatcher()
		if err == nil {
			defer watcher.Close()
			events = watcher.Events
			go func() {
				for range watcher.Errors {
				}
			}()
		}
		watching := false
		for {
			if watcher != nil && !watching {
				watching = watcher.Add(themesDir) == nil
			}
			wait := themesPoll
			if watching {
				wait = time.Hour // the notifications wake it
			}
			select {
			case event, ok := <-events:
				if !ok {
					events, watching, watcher = nil, false, nil
					continue
				}
				if filepath.Clean(event.Name) == filepath.Clean(themesDir) && event.Has(fsnotify.Remove|fsnotify.Rename) {
					watching = false // the folder went: looked at until it is back
				}
				// An editor saves in several steps: let them settle.
				time.Sleep(150 * time.Millisecond)
				for len(events) > 0 {
					<-events
				}
			case <-time.After(wait):
				if watching {
					continue
				}
			}
			next := themesStamp()
			if next == stamp {
				continue
			}
			stamp = next
			fyne.Do(func() {
				shaderBroken, shaderError = "", "" // perhaps fixed: tried again
				loadThemeFiles()
				ui.themeChanged()
			})
		}
	}()
}

// themesPoll is how often the folder is looked at without notifications.
const themesPoll = 5 * time.Second

// themeChoices are the theme settings to pick from, with their labels:
// the built-in ones, then the files.
func themeChoices(builtIn [][2]string) [][2]string {
	choices := builtIn
	for _, theme := range themeFiles {
		choices = append(choices, [2]string{theme.Setting(), theme.Name})
	}
	return choices
}

var unsafeName = regexp.MustCompile(`[^a-z0-9_-]+`)

// newThemeFile writes a theme file, under a name not taken yet, and opens
// it in the user's editor.
func (ui *desktopUI) newThemeFile(name, text string) {
	if err := os.MkdirAll(themesDir, 0o755); err != nil {
		ui.showError(err)
		return
	}
	base := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if base == "" {
		base = "theme"
	}
	path := filepath.Join(themesDir, base+".toml")
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = filepath.Join(themesDir, fmt.Sprintf("%s-%d.toml", base, i))
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		ui.showError(err)
		return
	}
	loadThemeFiles()
	ui.setTheme(palette.ThemeFilePrefix + strings.TrimSuffix(filepath.Base(path), ".toml"))
	ui.openThemePath(path)
}

// duplicateTheme copies the theme in use into a new file: a file's text as
// it is, a built-in theme as the template on its colours.
func (ui *desktopUI) duplicateTheme() {
	if theme, ok := themeFile(ui.theme.theme); ok && theme.Packaging() != "loose" {
		ui.unpackTheme(theme, theme.ID+"-copy", theme.Name+" copy")
		return
	} else if ok {
		data, err := os.ReadFile(theme.Path)
		if err != nil {
			ui.showError(err)
			return
		}
		text := regexp.MustCompile(`(?m)^name\s*=.*$`).ReplaceAllString(string(data), fmt.Sprintf("name = %q", theme.Name+" copy"))
		ui.newThemeFile(theme.ID+"-copy", text)
		return
	}
	text := palette.ThemeTemplate("My theme")
	if !themeTokens.Dark {
		text = strings.Replace(text, `base = "dark"`, `base = "light"`, 1)
		text = strings.Replace(text, `background = "#121212"`, fmt.Sprintf("background = %q", palette.Hex(palette.Light.Background)), 1)
		text = strings.Replace(text, `text = "#f4f4f4"`, fmt.Sprintf("text = %q", palette.Hex(palette.Light.Text)), 1)
	}
	ui.newThemeFile("my-theme", text)
}

// unpackTheme copies a zip or folder theme into a theme folder of the given
// name, ready to edit by hand, and selects it.
func (ui *desktopUI) unpackTheme(theme palette.ThemeFile, id, name string) {
	id = strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(id), "-"), "-")
	dest := filepath.Join(themesDir, id)
	if err := palette.UnpackTheme(theme.PackagePath(), dest); err != nil {
		ui.showError(err)
		return
	}
	tomlPath := filepath.Join(dest, filepath.Base(theme.Path))
	if name != theme.Name {
		if data, err := os.ReadFile(tomlPath); err == nil {
			text := regexp.MustCompile(`(?m)^name\s*=.*$`).ReplaceAllString(string(data), fmt.Sprintf("name = %q", name))
			os.WriteFile(tomlPath, []byte(text), 0o644)
		}
	}
	loadThemeFiles()
	ui.setTheme(palette.ThemeFilePrefix + id)
	ui.openThemePath(tomlPath)
}

// packTheme packs the loose theme in use into a zip.
func (ui *desktopUI) packTheme(theme palette.ThemeFile) {
	zipPath, err := palette.PackTheme(themesDir, theme.ID)
	if err != nil {
		ui.showError(fmt.Errorf("The theme was not packed: %v", err))
		return
	}
	platform.Devf("ui", "theme packed into %s", zipPath)
	loadThemeFiles()
	ui.themeChanged()
}

func (ui *desktopUI) openThemePath(path string) {
	if err := fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: path}); err != nil {
		ui.showError(err)
	}
}

func (ui *desktopUI) showError(err error) {
	ui.confirm("Theme", err.Error(), "OK", func() {})
}

// syncProjectTheme paints the theme bound to the project in view, or the
// global one when it has none.
func (ui *desktopUI) syncProjectTheme() {
	want := ui.projectThemes[ui.cfg.Workspace]
	if want == ui.theme.project {
		return
	}
	ui.theme.project = want
	ui.theme.apply()
	refreshRadii()
	ui.syncBackdrop()
	ui.renderChats()
}

// projectThemeAction binds a project to a theme.
func (ui *desktopUI) projectThemeAction(project string) menuAction {
	return menuAction{label: "Theme…", icon: blankIcon, run: func() {
		const global = "The global theme"
		picker := newPicker(ui.window, "Theme", styleField, nil)
		picker.Options = []string{global}
		choices := themeChoices(builtInThemes())
		for _, choice := range choices {
			picker.Options = append(picker.Options, choice[1])
		}
		picker.SetSelected(global)
		for _, choice := range choices {
			if choice[0] == ui.projectThemes[project] {
				picker.SetSelected(choice[1])
			}
		}
		var dialog *sheet
		picker.onChanged = func(label string) {
			value := ""
			for _, choice := range choices {
				if choice[1] == label {
					value = choice[0]
				}
			}
			if ui.projectThemes == nil {
				ui.projectThemes = map[string]string{}
			}
			if value == "" {
				delete(ui.projectThemes, project)
			} else {
				ui.projectThemes[project] = value
			}
			ui.save()
			ui.syncProjectTheme()
		}
		body := container.NewVBox(note("Only this project's chats use this theme."), picker.row)
		dialog = ui.modal("Theme of "+ui.projectName(project), body, 420, nil, newRow("Done", nil, stylePrimary, func() { dialog.dismiss() }))
	}}
}

// builtInThemes are Dark, Light and System, as the settings list has them.
func builtInThemes() [][2]string {
	themes, _ := registry.Lookup(registry.KeyTheme)
	var choices [][2]string
	for _, value := range themes.Values {
		choices = append(choices, [2]string{value.ID, value.Label})
	}
	return choices
}

// paintedRoundness is the roundness the window's corners are drawn at.
var paintedRoundness float32 = 1

// refreshRadii scales the corners already on screen to the theme's
// roundness; what is built from now on asks rounded().
func refreshRadii() {
	if roundness == paintedRoundness {
		return
	}
	scale := roundness / paintedRoundness
	paintedRoundness = roundness
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	for _, window := range app.Driver().AllWindows() {
		c := window.Canvas()
		roots := append([]fyne.CanvasObject{c.Content()}, c.Overlays().List()...)
		for _, root := range roots {
			fynedriver.WalkObjects(root, func(object fyne.CanvasObject) {
				if r, ok := object.(*canvas.Rectangle); ok {
					r.CornerRadius *= scale
					r.TopLeftCornerRadius *= scale
					r.TopRightCornerRadius *= scale
					r.BottomLeftCornerRadius *= scale
					r.BottomRightCornerRadius *= scale
					r.Refresh()
				}
			})
		}
	}
}

// themeFileControls offer New, Duplicate and Edit, and show the files that
// could not be read and why: the end of the Theme row, inset as its text.
func (ui *desktopUI) themeFileControls() fyne.CanvasObject {
	actions := container.NewHBox(
		newRow("New theme", nil, styleOutline, func() { ui.newThemeFile("my-theme", palette.ThemeTemplate("My theme")) }),
		newRow("Duplicate", nil, styleOutline, ui.duplicateTheme),
	)
	if file, ok := themeFile(ui.theme.themeSetting()); ok {
		switch file.Packaging() {
		case "loose":
			actions.Add(newRow("Edit", nil, styleOutline, func() { ui.openThemePath(file.Path) }))
			actions.Add(newRow("Pack into zip", nil, styleOutline, func() { ui.packTheme(file) }))
		case "folder":
			actions.Add(newRow("Edit", nil, styleOutline, func() { ui.openThemePath(file.Path) }))
		case "zip":
			actions.Add(newRow("Unpack to edit", nil, styleOutline, func() { ui.unpackTheme(file, file.ID+"-edit", file.Name) }))
		}
	}
	actions.Add(newRow("Open folder", nil, styleOutline, func() {
		os.MkdirAll(themesDir, 0o755)
		ui.openThemePath(themesDir)
	}))
	// Read as the row's second line of explanation, not the faint grey of
	// something disabled (#508).
	where := plainFlow("Themes are read from "+themesDir+". Zip, folder or .toml each. Format: docs/THEMES.md.", 12.5, colMuted)
	rows := container.New(layout.NewCustomPaddedVBoxLayout(8), where, actions)
	for _, failed := range themeFileErrors {
		problem := note(failed.Error())
		problem.Importance = widget.DangerImportance
		rows.Add(problem)
	}
	return padded(0, 13, 16, 16, rows)
}
