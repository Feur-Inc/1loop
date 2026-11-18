// SPDX-License-Identifier: MPL-2.0

package ui

// No background repository reads in tests: the test driver would run their
// sidebar update on the reading goroutine, racing the test (a CI failure,
// "concurrent map writes" in sidebar_glide.go). No test needs them.
func init() { startGitLoad = func(func()) {} }
