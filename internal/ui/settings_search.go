// SPDX-License-Identifier: MPL-2.0

package ui

// The settings' search: the box above the sections finds settings in the
// shared list (internal/settings) as it is typed, and a result opens the
// setting's page, scrolled to its row, which glows a moment. Enter opens
// the first one.

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
	"1loop/internal/gitrepo"
	registry "1loop/internal/settings"
)

// settingAnchor is a setting's row on its page, where a search leads.
type settingAnchor struct {
	glow *canvas.Rectangle
	box  *fyne.Container
	anim *fyne.Animation
}

// mark records the row of a setting of the shared list, for the search.
func (view *settingsView) mark(key string, row fyne.CanvasObject) fyne.CanvasObject {
	if _, ok := registry.Lookup(key); !ok {
		panic("settings: " + key + " is not in internal/settings")
	}
	glow := canvas.NewRectangle(color.Transparent)
	glow.CornerRadius = 10
	anchor := &settingAnchor{glow: glow, box: container.NewStack(glow, row)}
	view.anchors[key] = anchor
	return anchor.box
}

// find shows the settings matching what is typed, or the page again once
// the box is empty.
func (view *settingsView) find(query string) {
	if strings.TrimSpace(query) == "" {
		if view.searching && len(view.frames) > 0 {
			view.present()
		}
		return
	}
	matches := registry.Search(registry.On(registry.OnDesktop), query)
	page := container.NewVBox(view.pageTitle("Search", "", nil))
	if len(matches) == 0 {
		page.Add(card(settingRow("No setting matches “"+strings.TrimSpace(query)+"”", "Try another word, such as server, model, phone or browser: its name, what it does, or a synonym.", nil)))
	} else {
		rows := make([]fyne.CanvasObject, 0, len(matches))
		for _, match := range matches {
			rows = append(rows, view.result(match.Setting))
		}
		page.Add(card(rows...))
	}
	view.searching = true
	view.column.Objects = []fyne.CanvasObject{page}
	view.column.Refresh()
	view.bar.Hide()
	view.view.Refresh()
	view.scroll.ScrollToTop()
}

// result is a found setting: its title, where it is and what it does.
func (view *settingsView) result(setting registry.Setting) fyne.CanvasObject {
	where := registry.SectionTitle(setting.Section)
	if setting.Page != "" {
		where += " › " + setting.Page
	}
	text := container.New(layout.NewCustomPaddedVBoxLayout(3),
		plainFlow(setting.Title, 14, colText),
		plainFlow(where, 12, colFaint),
		plainFlow(setting.Description, 12.5, colMuted))
	key := setting.Key
	arrow := canvas.NewImageFromResource(tinted(iconChevronRight, colMuted))
	arrow.SetMinSize(fyne.NewSquareSize(14))
	row := padded(12, 12, 16, 16, container.NewBorder(nil, nil, nil, container.NewCenter(arrow), text))
	return container.NewStack(row, newTapArea(func() { view.jump(key) }))
}

// clearSearch empties the box without showing the page again: the caller
// is about to show one.
func (view *settingsView) clearSearch() {
	view.searching = false
	if view.search == nil || view.search.Text == "" {
		return
	}
	onChanged := view.search.OnChanged
	view.search.OnChanged = nil
	view.search.SetText("")
	view.search.OnChanged = onChanged
}

// jump opens the page of a setting and shows its row.
func (view *settingsView) jump(key string) {
	setting, ok := registry.Lookup(key)
	if !ok {
		return
	}
	page := registry.SectionTitle(setting.Section)
	if setting.Page != "" {
		page = setting.Page
	}
	view.confirmLeave(0, func() {
		view.showNow(page)
		anchor := view.anchors[key]
		if anchor == nil {
			return
		}
		// The rows have their places once the page is laid out, a frame later.
		time.AfterFunc(80*time.Millisecond, func() { fyne.Do(func() { view.reveal(anchor) }) })
	})
}

// reveal scrolls to a row and makes it glow, then fade.
func (view *settingsView) reveal(anchor *settingAnchor) {
	driver := fyne.CurrentApp().Driver()
	y := driver.AbsolutePositionForObject(anchor.box).Y - driver.AbsolutePositionForObject(view.scroll.Content).Y
	limit := view.scroll.Content.MinSize().Height - view.scroll.Size().Height
	view.scroll.ScrollToOffset(fyne.NewPos(0, max(0, min(y-24, limit))))
	if anchor.anim != nil {
		anchor.anim.Stop()
	}
	anchor.anim = fyne.NewAnimation(2200*time.Millisecond, func(p float32) {
		strength := float32(1)
		if p > 0.45 { // held, then fading
			strength = 1 - (p-0.45)/0.55
		}
		glow := colAmber
		glow.A = uint8(64 * strength)
		anchor.glow.FillColor = glow
		anchor.glow.Refresh()
	})
	anchor.anim.Start()
}

// settingsAccess is the settings tool's way to this computer's settings:
// those of the shared list it has, changed as their rows change them, on
// the interface's thread, the page shown again if it is open.
func (ui *desktopUI) settingsAccess() *agent.SettingsAccess {
	onOff := func(on bool) string {
		if on {
			return "on"
		}
		return "off"
	}
	return &agent.SettingsAccess{
		Get: func(ctx context.Context, key string) (value string, ok bool) {
			ui.onUI(ctx, func() {
				ok = true
				switch key {
				case registry.KeyMemory:
					value = onOff(!ui.memoryOff)
				case registry.KeyWrapUp:
					value = onOff(!ui.wrapUpOff)
				case registry.KeyDynamicCompaction:
					value = onOff(!ui.classicCompaction)
				case registry.KeyAutoResume:
					value = onOff(ui.autoResume != nil && ui.autoResume.Enabled)
				case registry.KeyTheme:
					value = ui.theme.themeSetting()
				case registry.KeyAccent:
					value = ui.theme.accentSetting()
				case registry.KeyWorktreeSort:
					value = ui.worktreeSort
					if sortLabels[value] == "" {
						value = gitrepo.SortRecent
					}
				default:
					if setting, known := registry.Lookup(key); known && setting.Kind == registry.Shortcut {
						value = ui.shortcutFor(key)
					} else {
						ok = false
					}
				}
			})
			return value, ok && ctx.Err() == nil
		},
		Set: func(ctx context.Context, key, value string) (err error) {
			ui.onUI(ctx, func() {
				on := value == "on"
				switch key {
				case registry.KeyMemory:
					ui.setMemory(on)
				case registry.KeyWrapUp:
					ui.setWrapUp(on)
				case registry.KeyDynamicCompaction:
					ui.setDynamicCompaction(on)
				case registry.KeyAutoResume:
					if err = ui.setAutoResume(on); err != nil {
						err = errors.New("Automatic resumption has no steps yet: the user chooses them in Settings first")
					}
				case registry.KeyTheme:
					ui.setTheme(value)
				case registry.KeyAccent:
					ui.setAccent(value)
				case registry.KeyWorktreeSort:
					ui.setWorktreeSort(value)
				default:
					if setting, known := registry.Lookup(key); known && setting.Kind == registry.Shortcut {
						err = ui.setShortcut(key, value, false)
					} else {
						err = fmt.Errorf("%s is not a setting of this computer", key)
					}
				}
				if setting, ok := registry.Lookup(key); ok && err == nil {
					page := registry.SectionTitle(setting.Section)
					if setting.Page != "" {
						page = setting.Page
					}
					if ui.prefs.at(page) {
						ui.prefs.show(ui.prefs.page) // the row shows the change
					}
				}
			})
			if err == nil {
				err = ctx.Err()
			}
			return err
		},
	}
}
