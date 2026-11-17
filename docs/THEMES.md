<!-- SPDX-License-Identifier: MPL-2.0 -->

# Theme files

1loop's desktop app reads themes of your own from a folder. Theme files are desktop only for now: the phone keeps its own Dark / Light / System setting and accent.


- Linux: `~/.config/1loop/themes/`
- macOS: `~/Library/Application Support/1loop/themes/`
- Windows: `%AppData%\1loop\themes\`

A theme is stored in one of three ways (see [One zip per theme](#one-zip-per-theme)): a `.zip` file, a folder laid out like the zip, or loose files (a `.toml` with its shader and fonts beside it). Each one there is a theme. It appears in **Settings → General → Theme** next to Dark, Light and System as soon as it is saved, without a restart, and the theme in use is painted again each time its file changes. A file that cannot be read is listed in red under the theme picker with the reason (and the line, for a syntax error); the other themes keep working, and a theme in use whose file breaks shows 1loop's dark look until it is fixed.

Under the picker:

- **New theme** writes a commented template (every key, 1loop's dark colours) and opens it in your editor.
- **Duplicate** copies the theme in use: a theme file as it is, a zip or folder as a folder, or Dark/Light as a template.
- **Edit** opens the theme file in use (for a zip, **Unpack to edit** copies it to a folder first).
- **Pack into zip** turns a loose theme in use into a zip.
- **Open folder** opens the themes folder.

A project can have its own theme: in the sidebar, the project's menu → **Theme…**. Its chats use that theme while they are on screen; the others keep the global one.

## One zip per theme

A theme with a shader, fonts and a licence is several files; as a zip they stay together. Put `<name>.zip` in the themes folder (the name is the theme's id, `file:<name>`). Inside, at the root:

```
midnight.zip
├── theme.toml          the theme (the only .toml, or the one called theme.toml)
├── aurora.frag         shaders
├── fonts/Maple.ttf     fonts, with their licence
├── fonts/LICENSE.txt
└── preview.png         optional picture
```

The theme refers to the other files by relative path inside the zip (`shader = "aurora.frag"`, `font = "fonts/Maple.ttf"`); each theme carries its own copy of a shared font and its licence. A **folder** `themes/<name>/` with the same layout is read the same way: it is the form to edit by hand, the zip is the form to share. Loose files keep working.

Adding, replacing or removing a zip or folder is picked up within a second. 1loop reads a zip in memory and never extracts it. Safety: an entry with an absolute path or `..` is refused, a theme may have at most 200 entries and 64 MB unpacked, and only `.toml`, shaders (`.frag`, `.glsl`), fonts (`.ttf`, `.otf`), images (`.png`, `.jpg`, `.webp`, `.gif`) and text files (`.txt`, `.md`, licences) are allowed. A broken zip is listed in red with the reason. Two themes with the same name (`a.zip` and `a.toml`) are an error: keep one.

## Format

```toml
# SPDX-License-Identifier: MPL-2.0
name = "Nord"            # as the picker shows it; the file name when left out
base = "dark"            # dark or light: the colours left out come from it
accents = ["#88c0d0", "#a3be8c"]
roundness = 1.0          # corners: 0 square, 1 1loop's own, up to 1.6
shader = "aurora.frag"   # a GLSL background, beside this file
shader_opacity = 0.8     # how much the background and sidebar cover it, 0 to 1
font = "IBM Plex Sans"   # the interface font: a family, or a .ttf/.otf
font_mono = "JetBrains Mono"

[colors]
background = "#2e3440"
sidebar = "#272c36"
text = "#eceff4"
danger = "#bf616a"
```

Every key is optional. Colours are `#rrggbb`, or `#rrggbbaa` with an alpha.

### accents

When a theme has accents, they replace the preset swatches of **Accent colour**. With one accent it is the accent, with nothing to choose (a theme made by matugen, for example); with several, the first is chosen when you switch to the theme and you can pick another. A colour of your own typed as `#rrggbb` still wins over them. Switching to a theme with accents resets the accent to its first one.

The accent is fitted to the background as before: lightened on dark or darkened on light until it reads.

### roundness

Scales the corners of everything rounded: cards, buttons, fields, the composer, code blocks, menus, dialogs, tooltips and message bubbles (thin bars and pills of a few pixels keep theirs). `0` to `1.6`; anything else is an error.

### shader

A GLSL fragment shader drawn behind the whole window: the background and the sidebar are laid over it at `shader_opacity` (0.8 by default; cards, menus and fields stay opaque). The path is relative to the theme file. Saving the shader updates the app at once, like the theme.

Write only the shader's body, without `#version`: 1loop adds the version for your GL (GLSL 1.10, or GLSL ES 1.00 with `precision mediump float`), so keep to what both accept (`gl_FragColor`, no `in`/`out`). 1loop declares these uniforms before your code; don't declare them again:

| uniform | |
| --- | --- |
| `vec2 u_resolution` | the window's size in pixels |
| `vec2 u_origin` | its bottom left corner in `gl_FragCoord`: `(gl_FragCoord.xy - u_origin) / u_resolution` goes from 0 to 1 |
| `float u_time` | seconds; it stops while the shader is paused |
| `vec4 u_background`, `u_surface`, `u_text`, `u_accent` | the theme's colours (opaque) and the accent in use |
| `float u_state` | what the agent does, over all chats: `STATE_IDLE`, `STATE_WORKING`, `STATE_WAITING` (an approval or a question) or `STATE_ERROR` |
| `float u_state_time` | seconds since `u_state` last changed |

**Settings → General → Shader background** sets the frame rate (15, 30 or 60 fps; 30 by default) or turns shaders off, which shows the theme's plain colours. The shader pauses while the window is hidden in the tray or not focused, and is still while Animations are off.

A shader that does not compile or link is not drawn: the theme's plain colours are shown, with the compiler's message in red in Settings → General. It is tried again as soon as a file in the folder changes.

Examples: [`themes/aurora.frag`](themes/aurora.frag) (reacts to the agent's state, used by `aurora.toml`) and [`themes/grain.frag`](themes/grain.frag) (used by `paper.toml`). `glslangValidator` can check a shader before you save it: put `#version 110` and the uniforms above in front of it.

### background_image

Instead of a shader, a picture drawn behind the whole window: `.png`, `.jpg`, `.webp`, or an animated `.gif`, which loops at its own frame delays. The path is relative to the theme file. It covers the window (cropped to keep its proportions), and the background and sidebar are laid over it at `shader_opacity`, like a shader. A theme has either `shader` or `background_image`, not both.

A GIF pauses like a shader: while the window is hidden or in the background, and while Animations are off. **Shader background → Off** hides the picture too and shows the theme's plain colours. A picture that can't be read is reported in red in Settings → General, like a shader that fails. Example: [`themes/drift.toml`](themes/drift.toml) with `drift.gif`.

### fonts

| key | |
| --- | --- |
| `font` | the interface's text |
| `font_bold` | bold text (headings, buttons); by default the bold of `font`'s family |
| `font_italic` | italic text; by default the italic of `font`'s family |
| `font_mono` | code, terminals, diffs, file contents |

Each is either a font file (`.ttf` or `.otf`; a relative path is beside the theme file) or a family installed on the system, as `fc-match` understands it: `"Fira Code"`, `"IBM Plex Sans:bold"`, `"Noto Serif:italic"`. A family that isn't installed, or a file that can't be read, keeps 1loop's font (Inter, or Fyne's own) for that style. Sizes don't change.

### font colours

Text colours are ordinary `[colors]` keys: `text` (body text), `text_soft`, `muted` (secondary text), `placeholder`, `faint` (hints, line numbers), `code_ink` (inline code), `danger` and `added` (error and success text). Links and highlighted text follow the accent.

### colors

| key | what it colours |
| --- | --- |
| `background` | the conversation, the window |
| `sidebar` | the sidebar and the title bar |
| `surface` | cards, dialogs, panels |
| `popover` | menus |
| `raised` | buttons, tooltips, headers in a card |
| `field` | text fields and select boxes |
| `code` | code blocks, terminals and diffs |
| `border`, `border_soft`, `border_hot`, `border_hover`, `pop_border` | borders, separators, outlines under the pointer, a menu's edge |
| `text`, `text_soft`, `muted`, `placeholder`, `faint` | text, from the loudest to the quietest |
| `track`, `knob_off` | a slider's empty track, a disabled knob |
| `danger`, `close`, `close_hot` | errors, destructive buttons |
| `added`, `diff_added`, `diff_removed` | success, diff lines' backgrounds |
| `code_ink` | inline code's text |
| `overlay` | laid translucent over surfaces for hover and selection (white on dark, black on light) |
| `hover`, `selected`, `menu_hover`, `pressed`, `scroll_bar`, `code_chip`, `code_edge`, `glass_edge` | the translucent washes themselves (use `#rrggbbaa`) |

Colours you leave out are not simply the base's: when you set `background`, the surfaces and borders you left out are placed between it and the text as far as the base's are, so a blue-grey background gets blue-grey cards rather than near-black ones. Likewise `muted`, `placeholder` and the other quiet texts follow `text`.

Unknown keys are errors, so a typo is reported rather than ignored.

## The agent

The agent reads a short copy of this format on demand (`internal/palette/theme_format.md`, the settings tool's `theme_format`), writes a theme and its shader with `theme_write` (as `<name>.zip`; a theme that is already a folder or loose files is replaced where it is) (checked first, errors given back in plain words, saved only after you approve it), and switches to it with the settings tool: `general.theme` takes `file:<name>` for `<name>.toml`.

## matugen

[matugen](https://github.com/InioX/matugen) can write a theme from your wallpaper. Copy [`themes/matugen.toml.template`](themes/matugen.toml.template) to `~/.config/matugen/templates/1loop.toml` and add to `~/.config/matugen/config.toml`:

```toml
[templates.oneloop]
input_path = "~/.config/matugen/templates/1loop.toml"
output_path = "~/.config/1loop/themes/matugen.toml"
```

Each time matugen runs, 1loop picks the new colours up. For a light wallpaper scheme, change `base` to `"light"` in the template.

More examples: [`themes/nord.toml`](themes/nord.toml), [`themes/paper.toml`](themes/paper.toml), [`themes/aurora.toml`](themes/aurora.toml).

## Not there yet

- The phone takes the desktop's theme files (colours, accents, roundness). Shaders would stay desktop only.
