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
  a data device of its own: with that second device, GNOME (Mutter) most
  often left GLFW's without the selection, so text could not be pasted. `glfwGetClipboardString` returns no
  text for a selection that is only an image.
- `glfw/src/wl_window.c`, `wl_platform.h` (#431): on Wayland the window
  takes `GLFW_X11_CLASS_NAME` as its app id, sent with its xdg toplevel
  before the first commit (GLFW 3.4 has `GLFW_WAYLAND_APP_ID` for this).
  1loop sets the app id itself only once the window was shown, so GNOME
  first took it for an unknown application, with a generic icon.
- `build.go` (#412): Windows no longer links `opengl32.dll` (`-lopengl32`):
  GLFW already loads it by name when it starts WGL (`wgl_context.c`), and
  then gets the one 1loop loaded, Mesa's on a PC without an OpenGL driver
  (internal/softgl).
- `glfw/src/wl_window.c`, `wl_init.c`, `wl_platform.h`, the generated
  `wayland-fractional-scale-v1-client-protocol.{c,h}` (wayland-scanner
  1.23.1, from wayland-protocols' staging `fractional-scale-v1.xml`) and
  `c_glfw_lin.go`, `c_glfw_bsd.go`: on a compositor with
  `wp_fractional_scale_manager_v1` and `wp_viewporter`, the window draws its
  buffer at the scale the compositor prefers (1.25 for 125 %) with buffer
  scale 1, and a viewport gives it its size; content scale and framebuffer
  size follow. GLFW 3.3 only knew integer scales: at 125 % on GNOME it drew
  at 2 and Mutter shrank every frame to 1.25, so text and icons were soft,
  more or less depending on where they fell on the pixel grid (sharper while
  hovered, a pixel's move changing them). GLFW 3.4 has the same support.
- `glfw/src/wl_window.c`, `wl_init.c`, `wl_platform.h`,
  `include/GLFW/glfw3native.h`, `wayland-primary-selection-unstable-v1-client-protocol.{c,h}`
  (written in wayland-scanner's layout from wayland-protocols'
  `unstable/primary-selection/primary-selection-unstable-v1.xml`, only what
  GLFW uses) and `c_glfw_lin.go`, `c_glfw_bsd.go` (1loop #626): on a
  compositor with `zwp_primary_selection_device_manager_v1`, GLFW gets a
  primary selection device for its seat, and the native
  `glfwSetWaylandPrimarySelection` offers text as the primary selection
  (what a middle click pastes), with the serial of the last input. The
  offers of other programs are only released: nothing reads them.
- `glfw_tree_rebuild.go`: `upstreamTreeSHA` is the git tree of `glfw/` after
  the changes above, so that a build cache made before it compiles the C
  sources again (Go does not hash them).
