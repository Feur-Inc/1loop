Theme file format (1loop desktop). A theme is `<name>.zip` in the themes folder: `theme.toml` plus an optional `.frag` shader (and fonts, licence, preview; paths relative inside the zip, no `..`). A folder `<name>/` with the same layout, or a loose `<name>.toml` with files beside it, also work. theme_write creates the zip for you. Every key is optional; colours are `#rrggbb` (`#rrggbbaa` for washes). Unknown keys are errors.

```toml
name = "Nord"             # shown in the picker
base = "dark"             # dark or light: colours left out come from it
accents = ["#88c0d0"]     # one or more; the first is chosen
roundness = 1.0           # corners, 0 to 1.6 (1 = default)
shader = "aurora.frag"    # GLSL background beside the theme (or background_image, not both)
background_image = "wall.png"  # .png .jpg .webp .gif, beside the theme
shader_opacity = 0.8      # 0 to 1: how much background and sidebar cover it
font = "IBM Plex Sans"    # also font_bold, font_italic, font_mono: an installed family or a .ttf/.otf beside
[colors]
background = "#2e3440"    # the window; surfaces and borders left out are derived from it
sidebar = "#272c36"
surface = "#3b4252"       # cards, dialogs
text = "#eceff4"          # quieter texts (muted, placeholder, faint) follow it
danger = "#bf616a"
```

Other colours: popover, raised, field, code, border, border_soft, border_hot, border_hover, pop_border, text_soft, muted, placeholder, faint, track, knob_off, close, close_hot, added, diff_added, diff_removed, code_ink, overlay, hover, selected, menu_hover, pressed, scroll_bar, code_chip, code_edge, glass_edge.

Shader: only the body of a GLSL fragment shader, no `#version`, GLSL 1.10 / ES 1.00 (`gl_FragColor`, no `in`/`out`) with a `void main()`. These uniforms are already declared; do not declare them: `vec2 u_resolution`, `vec2 u_origin` (uv = (gl_FragCoord.xy - u_origin) / u_resolution), `float u_time`, `vec4 u_background`, `u_surface`, `u_text`, `u_accent`, `float u_state` (STATE_IDLE, STATE_WORKING, STATE_WAITING, STATE_ERROR), `float u_state_time`. A shader that fails to compile is not drawn and the error shows in Settings → General.

Applying: after theme_write, set `general.theme` to `file:<name>`. A theme per project is the user's choice (the project's menu → Theme…).
