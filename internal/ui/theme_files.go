// SPDX-License-Identifier: MPL-2.0

package ui

// Theme files (issue #464): the themes of ~/.config/1loop/themes, read
// again as soon as one is added or saved, their accents in place of the
// presets, their corner roundness, and a theme per project.

import (
	"fmt"
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
	"fyne.io/fyne/v2/widget"

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

// themesStamp sums up the folder: names, sizes and times of its files.
func themesStamp() string {
	paths, _ := filepath.Glob(filepath.Join(themesDir, "*.toml"))
	var b strings.Builder
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", path, info.Size(), info.ModTime().UnixNano())
		}
	}
	return b.String()
}

// watchThemes reads the theme files, then looks at the folder every second
// and reads it again when a file is added, saved or removed: the picker
// lists the new file, and the theme in use is painted again.
func (ui *desktopUI) watchThemes() {
	loadThemeFiles()
	stamp := themesStamp()
	go func() {
		for range time.Tick(time.Second) {
			next := themesStamp()
			if next == stamp {
				continue
			}
			stamp = next
			fyne.Do(func() {
				loadThemeFiles()
				ui.themeChanged()
			})
		}
	}()
}

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
	if theme, ok := themeFile(ui.theme.theme); ok {
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
		body := container.NewVBox(note("This project's chats use this theme while they are on screen; the others keep the global one."), picker.row)
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

// themeFileRow offers New, Duplicate and Edit, and shows the files that
// could not be read and why.
func (ui *desktopUI) themeFileRow() fyne.CanvasObject {
	actions := container.NewHBox(
		newRow("New theme", nil, styleOutline, func() { ui.newThemeFile("my-theme", palette.ThemeTemplate("My theme")) }),
		newRow("Duplicate", nil, styleOutline, ui.duplicateTheme),
	)
	if file, ok := themeFile(ui.theme.themeSetting()); ok {
		actions.Add(newRow("Edit", nil, styleOutline, func() { ui.openThemePath(file.Path) }))
	}
	actions.Add(newRow("Open folder", nil, styleOutline, func() {
		os.MkdirAll(themesDir, 0o755)
		ui.openThemePath(themesDir)
	}))
	rows := container.NewVBox(note("Theme files are read from "+themesDir+" and update the app as soon as they are saved. Format: docs/THEMES.md."), actions)
	for _, failed := range themeFileErrors {
		problem := note(failed.Error())
		problem.Importance = widget.DangerImportance
		rows.Add(problem)
	}
	return rows
}
