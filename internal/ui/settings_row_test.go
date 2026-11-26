// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// A row with no explanation is its title alone: it is shorter than the same
// row with one, and leaves no gap under the title.
func TestSettingRowWithoutDetail(t *testing.T) {
	test.NewTempApp(t)
	alone := settingRow("Animations", "", nil).MinSize().Height
	explained := settingRow("Animations", "Short motion as things change.", nil).MinSize().Height
	if alone >= explained {
		t.Fatalf("a row without a detail is %v high; with one it is %v", alone, explained)
	}
	if want := settingRow("Animations", "", nil).MinSize().Height; alone != want {
		t.Fatalf("the same title lays out to %v, then %v", want, alone)
	}
}

func TestSettingStackWithoutDetail(t *testing.T) {
	test.NewTempApp(t)
	below := widget.NewLabel("control")
	alone := settingStack("Project folder", "", below).MinSize().Height
	explained := settingStack("Project folder", "Opens a folder.", below).MinSize().Height
	if alone >= explained {
		t.Fatalf("a stack without a detail is %v high; with one it is %v", alone, explained)
	}
}
