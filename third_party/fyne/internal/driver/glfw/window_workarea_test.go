//go:build !wasm && !test_web_driver

package glfw

import "testing"

// 1loop #512: a window never goes under the screen's panels.
func TestFitArea(t *testing.T) {
	for _, c := range []struct {
		name                           string
		width, height, areaW, areaH    int
		screenW, screenH, wantW, wantH int
	}{
		{"under a 54 px panel", 1180, 760, 1280, 746, 1280, 800, 1180, 746},
		{"a large screen leaves it alone", 1180, 760, 2560, 1400, 2560, 1440, 1180, 760},
		{"no work area smaller than the screen: a tenth left", 1180, 760, 1280, 800, 1280, 800, 1152, 720},
		{"no work area at all", 1180, 760, 0, 0, 1280, 800, 1152, 720},
		{"nothing known", 1180, 760, 0, 0, 0, 0, 1180, 760},
		{"never enlarged", 600, 400, 1280, 746, 1280, 800, 600, 400},
	} {
		if w, h := fitArea(c.width, c.height, c.areaW, c.areaH, c.screenW, c.screenH); w != c.wantW || h != c.wantH {
			t.Errorf("%s: %d×%d, want %d×%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
}
