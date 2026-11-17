# fyne-io/terminal, 1loop's copy

fyne-io/terminal at v0.0.0-20251106165109-867da7c64268 (BSD 3-Clause, see
LICENSE), copied from the module cache without its tests, test data,
images and the fyneterm command, and wired in with a `replace` in 1loop's
go.mod. It is the side panel's terminal (internal/termwidget). The first
commit of this directory is the untouched upstream copy, so `git diff`
against it shows every change.

Upstream's later versions fix most of the crashes below, but build only on
Fyne 2.8, and 1loop's Fyne is its own copy of 2.7.4 (third_party/fyne).
Moving to them means moving Fyne first; this copy can then go.

## Changes

- `escape.go`, `output.go`, `osc.go` (#450): output from the program in the
  terminal no longer crashes the app. Each handler keeps its counts and the
  cursor inside the line, the screen and the scroll area:
  - CSI P (delete characters) with the cursor past the line's text;
  - CSI @ (insert characters) past the line's text, on a row not yet
    made, or with a count past the right edge (it allocated it all);
  - CSI L and CSI S with huge counts (a loop as long as the count);
  - CSI r outside the screen, then ESC M or ESC D (rows indexed past the
    grid), and ESC M on a new terminal;
  - ESC 8 restoring a cursor saved on a screen since made smaller;
  - CSI h / CSI l without a mode, a final character of more than a byte,
    CSI > c / CSI = c without a value;
  - OSC 7 with a short URI. OSC 7 also no longer changes the whole app's
    working directory: it sets the terminal's `Config.PWD`, as upstream
    does now.
  ESC 7, ESC 8, ESC D and ESC M now run on Fyne's thread, in order with
  the rest of the output, like the other escapes.
- `output.go` (#450): a safety net. Every piece of output is shown under a
  `recover` (`Terminal.do`, and `handleOutput` itself): a handler that still
  panics drops that output and logs it with its stack, and the terminal
  goes on.
