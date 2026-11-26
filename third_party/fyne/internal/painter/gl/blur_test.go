// SPDX-License-Identifier: MPL-2.0
// 1loop addition (issue #464).

package gl

import "testing"

func TestBlurUsable(t *testing.T) {
	for _, c := range []struct {
		version, renderer string
		want              bool
	}{
		{"4.6.0 NVIDIA 550.54", "NVIDIA GeForce RTX 3060", true},
		{"4.6 (Compatibility Profile) Mesa 24.1", "AMD Radeon RX 6600 (radeonsi)", true},
		{"4.5 (Compatibility Profile) Mesa 24.1", "llvmpipe (LLVM 17.0.6, 256 bits)", false},
		{"2.1 Mesa 10.0", "Mesa DRI Intel(R) 945GM", false},
		{"OpenGL ES 2.0 Mesa", "Mali-400", true},
		{"1.1.0", "GDI Generic", false},
		{"", "", false},
	} {
		if got := blurUsable(c.version, c.renderer); got != c.want {
			t.Errorf("blurUsable(%q, %q) = %v, want %v", c.version, c.renderer, got, c.want)
		}
	}
}
