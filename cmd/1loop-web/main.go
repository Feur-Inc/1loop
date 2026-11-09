// SPDX-License-Identifier: MPL-2.0

// The browser client: 1loop's remote mode built for GOOS=js GOARCH=wasm,
// served by 1loop-server -web-dir at /app/ (make web).
package main

import "1loop/internal/ui"

func main() {
	ui.WebMain()
}
