// SPDX-License-Identifier: MPL-2.0

package ui

import "github.com/rymdport/portal/settings"

// The settings portal's appearance namespace has reduced-motion (1:
// reduce); desktops whose portal does not have it yet still pass on their
// own switch: GNOME's enable-animations, KDE's animation speed at 0.
var reducedMotionKeys = []struct{ namespace, key string }{
	{"org.freedesktop.appearance", "reduced-motion"},
	{"org.gnome.desktop.interface", "enable-animations"},
	{"org.kde.kdeglobals.KDE", "AnimationDurationFactor"},
}

// reducedMotionValue reads one of reducedMotionKeys' values; known is
// false for a value that says nothing.
func reducedMotionValue(key string, value any) (reduced, known bool) {
	switch value := value.(type) {
	case uint32:
		if key == "reduced-motion" {
			return value == 1, true
		}
	case bool:
		if key == "enable-animations" {
			return !value, true
		}
	case float64:
		if key == "AnimationDurationFactor" {
			return value == 0, true
		}
	}
	return false, false
}

// followReducedMotion reports the preference, then each change of it. It
// returns when there is no portal (no session bus, a bare window manager).
func followReducedMotion(report func(bool)) {
	source := ""
	for _, entry := range reducedMotionKeys {
		value, err := settings.ReadOne(entry.namespace, entry.key)
		if err != nil {
			continue
		}
		if reduced, known := reducedMotionValue(entry.key, value); known {
			source = entry.namespace + entry.key
			report(reduced)
			break
		}
	}
	_ = settings.OnSignalSettingChanged(func(changed settings.Changed) {
		if reduced, known := reducedMotionValue(changed.Key, changed.Value); known && (source == "" || source == changed.Namespace+changed.Key) {
			source = changed.Namespace + changed.Key
			report(reduced)
		}
	})
}
