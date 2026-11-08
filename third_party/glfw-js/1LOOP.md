# glfw-js in 1loop

github.com/fyne-io/glfw-js v0.3.0 (MIT, see LICENSE), Fyne's GLFW for the
browser, used only by the browser client (`make web`, issue #267). Changes:

- `browser_wasm.go` keydown: a typed character is one *rune*, not one byte
  (é, ü, ✓ were dropped), and AltGr types even when the browser also
  reports Ctrl+Alt (Windows).
- `textinput_wasm.go` (new), `browser_wasm.go`: text comes through a hidden
  `<textarea>`, focused while the application says a text field has the
  keyboard (`SetTextInput`, with the caret's place for the input method's
  candidate window). Its `input` and `composition*` events go to the char
  callback, so dead keys, input methods (Japanese, Chinese, Korean) and
  phone keyboards type; the composition shows in the field as it changes
  (Backspaces, then the new text). keydown leaves the keys it would type,
  and those of a composition, to the textarea. A tap on one of the
  `SetTextInputTargets` focuses it during the tap (pointerup), which a
  phone needs to open its keyboard. 1loop's side: `internal/ui/textinput_js.go`.
