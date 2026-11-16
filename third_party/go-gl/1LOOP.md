# go-gl/gl, 1loop's copy

go-gl/gl at v0.0.0-20231021071112-07e5d0ea2e71 (MIT licence, see LICENSE),
only the two packages Fyne builds on (`v2.1/gl`, `v3.1/gles2`), copied from
the module cache and wired in with a `replace` in 1loop's go.mod. The first
commit of this directory is the untouched upstream copy, so `git diff`
against it shows every change.

## Changes

- `v2.1/gl/package.go`, `v2.1/gl/procaddr.go` (#412): Windows no longer
  links `opengl32.dll` (`-lopengl32`). Linked, Windows loads it as the
  process starts, before any Go code, from the exe's folder or the system's:
  1loop could not choose Mesa's CPU renderer, from its own folder, on a PC
  without an OpenGL driver (internal/softgl). `GlowGetProcAddress` takes
  `wglGetProcAddress` from the `opengl32.dll` the process has loaded.
