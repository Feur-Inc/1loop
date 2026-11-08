# glfw-js in 1loop

github.com/fyne-io/glfw-js v0.3.0 (MIT, see LICENSE), Fyne's GLFW for the
browser, used only by the browser client (`make web`, issue #267). Changes:

- `browser_wasm.go` keydown: a typed character is one *rune*, not one byte
  (é, ü, ✓ were dropped), and AltGr types even when the browser also
  reports Ctrl+Alt (Windows).
