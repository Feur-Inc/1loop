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
- `internal/driver/glfw/window_desktop.go` (scaled): on Wayland, a change of
  content scale also throws the text textures away, as RescaleContext does.
  They are kept by text and colour, so text drawn before the compositor's
  fractional scale arrived was stretched from 1.0 to 1.25.
- `internal/painter/gl/draw.go` (roundToPixelCoords): a texture's
  rectangle keeps the size its texture is drawn at (`round(size × scale)`)
  instead of the distance between its rounded edges, which changed with
  its position at a fractional scale: at 1.25 the logo's ring (26 pixels)
  was drawn 26×25, stretched and resampled out of round, and icons and text
  were resampled by a pixel. `round_test.go` checks it.
- `scrollbar/` (new), `internal/widget/scroller.go` (1loop #633): every
  scroll (container.Scroll, List, Table, Tree, a multi-line Entry) has
  1loop's bar, the one the conversation's own bar (#625) also draws from
  the `scrollbar` package, so the two share their sizes, geometry and look.
  The bar's area takes the pointer over 16 px, wider than the thumb it
  draws, 4 px from the scroll's edge and from its track's ends (1loop's
  resize handles and the window's resize edges reach 4 px over what lies
  beside them). The thumb is never shorter than 40 px; under the pointer
  it widens from `SizeNameScrollBarSmall` (6) to `SizeNameScrollBar` (10)
  in `scrollbar.ColorNameHover` over a track in
  `ColorNameScrollBarBackground`. Dragging the thumb or the track scrolls
  (from the track, the thumb comes under the pointer by its middle); a tap
  on the track pages, keeping 40 px of the view, on every platform
  (`scroller_behavior_*.go`, which jumped there except on macOS, are
  gone); the wheel over the bar goes on to the scroll. Where both bars
  show, each ends before the other's corner. A theme giving
  `SizeNameScrollBar` 0 has no bar. `Scroll.Bars` gives the bar areas that show,
  for 1loop's text selection, which lies over code blocks and passes the
  pointer on to them. `container/scroll_1loop_test.go` and
  `scrollbar/scrollbar_test.go` check it.
