# Fyne, 1loop's copy

Fyne v2.7.4 (BSD 3-Clause, see LICENSE), copied from the module cache
without its tests, test data and command-line tools, and wired in with a
`replace` in 1loop's go.mod. 1loop changes it where measurements show the
interface spends its time; each change is listed here, and the first commit
of this directory is the untouched upstream copy, so `git diff` against it
shows every change.

## Changes

- `driver/frametimes.go`, `internal/driver/glfw/loop.go`: `driver.FrameTimes`
  reports how long each redraw's parts take (1loop's benchmarks).
- `minsize_memo.go`, `container.go`, `widget/widget.go`,
  `internal/driver/common/canvas.go`: during the per-frame minimum-size
  pass each object's MinSize is computed once, not once per tree level.
- `internal/driver/common/structures.go`, `canvas.go` (walkTree): when the
  visible children of an object change, the render nodes after the change
  are kept (and those of hidden objects kept aside for their return)
  instead of being rebuilt, which also re-laid out their parents.
- `internal/build/migrated_notfynedo.go`: thread checks off for every build
  (the `migrated_fynedo` behaviour); they read the goroutine's stack at
  every refresh.
- `internal/painter/image.go` (scaleImage): no resampling on the CPU for
  an enlargement or a reduction under a quarter; the GPU's linear filter
  does as well. A browser frame a pixel off its place was resampled with
  CatmullRom at every frame (browser panel: 90 % → 39 % of a core).
- `internal/cache/base.go`, `internal/driver/glfw/loop.go`: the caches'
  clock is the time read once per turn of the driver's loop (cache.Tick),
  not the system clock at every object stamped (microseconds a reading
  where the kernel times with HPET: half of an idle window's CPU). This
  replaces 1loop's former clock.go, which reached the same variable with
  go:linkname.
- `internal/driver/glfw/loop.go`, `loop_desktop.go`, `loop_wasm.go`,
  `window_desktop.go`: the driver's loop turns at the fastest screen's
  refresh rate (glfw video modes, read again when a screen is plugged or
  unplugged, 60 to 360 Hz) for half a second after input (pointer, wheel,
  keys) or after something has moved for 150 ms (frames drawn back to
  back), and at 60 Hz otherwise, so a 120 or 165 Hz screen gets every
  refresh while it matters and idle costs what it did. The caret's short
  fade doesn't count. The browser keeps 60.
- `internal/painter/font.go` (walkString): a tab is found at its index in
  the whole text, not in the run of one font it is in. After a change of
  font (an emoji between words), each tab shaped a range starting too
  early, so text before it was drawn again, partly in the emoji's font
  (1loop #438: a copied table row pasted in the composer looked broken).
  `internal/painter/font_tab_test.go` checks it.
- `canvas/blur.go`, `internal/painter/gl/blur*.go`, `draw.go`, `painter.go`
  (#464): `canvas.Blur`, a backdrop filter. The GL painter copies the
  pixels already drawn under it, blurs them with dual Kawase passes into
  framebuffer objects at halving sizes, and draws them back clipped to its
  rounded corners. Off (`canvas.BlurSupported`) on CPU renderers and GL
  older than 3.0. The painter now remembers its clip and output size.
- `canvas/shader.go` (`Watch`, `Uses`), `internal/painter/gl/shader_user.go`
  (#464): the painter says whether a shader uses given uniforms.
- `driver/desktop/pointer.go`, `internal/driver/glfw/window_desktop.go`
  (#464): `desktop.OnPointer` hears every pointer move, press and exit over
  a window, whatever object is under it.
- `internal/driver/glfw/window.go` (Resize), `window_desktop.go`,
  `window_wasm.go`: a window is never sized larger than the work area of
  its screen (glfw's work area, the screen less its panels), or than nine
  tenths of the screen where the platform gives no smaller work area
  (1loop #512: the first 1180×760 window went under a 54 px panel on a
  1280×800 screen). The window is also created at that size (primary
  screen): on Wayland the compositor keeps the size a window is created
  with, and the canvas was stretched over it. `window_workarea_test.go`
  checks the arithmetic.
- `driver/frametimes.go` (`TurnHook`, `Refreshes`, `KeepFonts`),
  `internal/driver/glfw/loop.go`, `canvas/canvas.go`,
  `widget/entry_cursor_anim.go`, `internal/cache/base.go`, `theme.go`
  (#474, from #489): 1loop's frame hook runs at each turn of the loop
  instead of as a never-ending animation; `canvas.Refresh` counts the
  refreshes asked (the hook's safety redraw only follows a window that
  changed); the caret stops blinking 10 s after the last key or focus, and
  stays solid with Animations off (`widget.CaretBlinks`); with `KeepFonts`
  a theme set again keeps the shaped text, and `ThemeOverride` resets the
  themed icons once per apply instead of every cache once per widget
  (`cache.ResetThemeColourCaches`).
- `internal/cache/canvases.go` (#474): `SetCanvasForObject` renews an
  entry already there in place. It kept it as it was (`LoadOrStore`), so
  the entries of objects nobody refreshed expired after a minute of frames,
  and it allocated an entry per object per frame only to drop it. 1loop
  walked the whole window every 5 s to renew them; it no longer does.
- `internal/painter/gl/retained.go`, `blur_gl_core.go`, `blur_gl_es.go`
  (`BlitToWindow`), `painter.go`, `internal/driver/glfw/damage.go`,
  `canvas.go` (`paintArea`), `loop.go` (`repaintWindow`),
  `internal/driver/common/canvas.go` (`TakeDamage`, `MaxDamaged`),
  `driver/partialframes.go` (#474): the retained frame. A frame is painted
  into a texture kept from one frame to the next and blitted into the
  window (GL 3 / GL ES 3; elsewhere painted straight into the window as
  before). When the only objects refreshed since the last frame are a few
  small shapes (rectangles, circles, lines: the caret) found in the window
  and not under a blur, the next frame clears and paints only their area,
  every clip within it and shapes far from it skipped; `SetDirty` (moves,
  resizes, layouts) and any other refresh paint the frame whole. Frames
  closer than 50 ms apart that follow a whole one (a shader, a scroll) are
  painted straight into the window: the blit costs a pass over it, 2 ms a
  frame on llvmpipe. `driver.PartialFrames` counts the frames painted in
  part. A clip outside the area is empty (`StartClipping` takes no
  negative size).
- `internal/driver/glfw/window_desktop.go` (`InFront`, #474): a window
  says whether it has the keyboard focus and is not minimised, from its own
  focus and iconify events, whichever window of the app had the focus
  before; 1loop's shader background pauses otherwise.
- `driver/recoloured.go`, `internal/driver/glfw/loop.go` (`recoloured`),
  `internal/cache/texture_common.go` (`TextureCanvas`) (#474): a change of
  colours with the fonts kept is told to the driver instead of setting the
  theme again, which laid out and refreshed every object and so dropped
  every texture (each picture resampled on the CPU). The driver clears the
  themed icons, drops the textures of the objects it is given and paints
  the windows again. `widget/entry_cursor_anim.go`: the caret's blink reads
  the primary colour again when it changes.
- `minsize_memo.go`, `internal/painter/gl/floats.go`, `draw.go`,
  `gl_core.go`, `gl_es.go` (#474): fewer allocations per frame. The
  minimum-size memory's map is emptied and used again instead of made at
  each frame and each layout; vertex data goes through one buffer per
  painter, uploaded without `gl.Ptr` (which boxed each slice). A full frame:
  355 → 135 allocations.
