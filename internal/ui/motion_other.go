// SPDX-License-Identifier: MPL-2.0

//go:build !linux && !windows && !js

package ui

import (
	"os/exec"
	"strings"
	"time"
)

// followReducedMotion reads macOS's Reduce motion (Accessibility,
// Display), again every half minute.
func followReducedMotion(report func(bool)) {
	last := ""
	for {
		out, err := exec.Command("defaults", "read", "com.apple.universalaccess", "reduceMotion").Output()
		if err != nil {
			out = []byte("0") // never set
		}
		if value := strings.TrimSpace(string(out)); value != last {
			last = value
			report(value == "1")
		}
		time.Sleep(30 * time.Second)
	}
}
