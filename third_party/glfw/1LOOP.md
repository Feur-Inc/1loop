# go-gl/glfw, 1loop's copy

go-gl/glfw v3.3 at v0.0.0-20240506104042-037f3cc74f2a (GLFW 3.3.10, zlib
licence, see LICENSE and glfw/LICENSE.md), copied from the module cache
without its test programs, and wired in with a `replace` in 1loop's go.mod.
Fyne's driver builds on it. The first commit of this directory is the
untouched upstream copy, so `git diff` against it shows every change.

## Changes

- `glfw/src/wl_window.c` (#145): the `wl_data_offer` listener handles every
  event (`source_actions` and `action` do nothing). GLFW binds the data
  device at version 1, which has neither, but niri sends `source_actions`
  during a drag, and libwayland aborts the process on an event without a
  handler: dragging anything into the window killed 1loop. The drag enter
  handler also no longer dereferences a NULL window (a surface that is not
  GLFW's) or a NULL surface.
- `glfw/src/wl_window.c`, `wl_platform.h`, `include/GLFW/glfw3native.h`
  (#334): the data device also notes the image type a selection is offered
  in (image/png first), keeps a selection that is only an image, and two
  native functions read it: `glfwGetWaylandClipboardImageType` and
  `glfwReceiveWaylandClipboard`. 1loop pastes images through them instead of
  a data device of its own: Mutter (GNOME) tells the selection to only one
  data device per client, and the second one left GLFW's without it, so
  text could not be pasted on GNOME. `glfwGetClipboardString` returns no
  text for a selection that is only an image.
- `glfw_tree_rebuild.go`: `upstreamTreeSHA` is the git tree of `glfw/` after
  the change above, so that a build cache made before it compiles the C
  sources again (Go does not hash them).
