// SPDX-License-Identifier: MPL-2.0

package ui

import "syscall/js"

// followReducedMotion follows the browser's prefers-reduced-motion.
func followReducedMotion(report func(bool)) {
	query := js.Global().Call("matchMedia", "(prefers-reduced-motion: reduce)")
	report(query.Get("matches").Bool())
	query.Call("addEventListener", "change", js.FuncOf(func(_ js.Value, args []js.Value) any {
		report(len(args) > 0 && args[0].Get("matches").Bool())
		return nil
	}))
}
