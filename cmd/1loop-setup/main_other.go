// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package main

import "fmt"

func main() {
	fmt.Println("1loop-setup " + version + " is the Windows installer: it runs on Windows only")
}
