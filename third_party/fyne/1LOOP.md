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
