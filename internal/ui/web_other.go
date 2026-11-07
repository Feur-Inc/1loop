// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"fmt"
	"os"
)

// WebMain is the browser client's (web_js.go): elsewhere, the desktop is.
func WebMain() {
	fmt.Fprintln(os.Stderr, "1loop-web runs in a browser: build it with GOOS=js GOARCH=wasm (make web) and serve it with 1loop-server -web-dir.")
	os.Exit(2)
}
