// SPDX-License-Identifier: MPL-2.0

//go:build !js

package cookieimport

// Firefox's cookie database driver. The browser client (GOOS=js) has no
// browser profiles to read and modernc's libc does not build there, so
// readFirefox's sql.Open fails cleanly instead.
import _ "modernc.org/sqlite"
