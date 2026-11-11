// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var systemParametersInfo = windows.NewLazySystemDLL("user32.dll").NewProc("SystemParametersInfoW")

// followReducedMotion reads Windows' "Animation effects" switch
// (SPI_GETCLIENTAREAANIMATION), again every few seconds: Windows tells
// windows of the change only through a message GLFW keeps to itself.
func followReducedMotion(report func(bool)) {
	const spiGetClientAreaAnimation = 0x1042
	last := -1
	for {
		var on int32 = 1
		if ret, _, _ := systemParametersInfo.Call(spiGetClientAreaAnimation, 0, uintptr(unsafe.Pointer(&on)), 0); ret != 0 {
			if reduced := btoi(on == 0); reduced != last {
				last = reduced
				report(reduced == 1)
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
