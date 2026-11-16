<!-- SPDX-License-Identifier: MPL-2.0 -->

# Theme files

1loop's desktop app reads themes of your own from a folder:

- Linux: `~/.config/1loop/themes/`
- macOS: `~/Library/Application Support/1loop/themes/`
- Windows: `%AppData%\1loop\themes\`

Each `*.toml` file there is a theme. It appears in **Settings → General → Theme** next to Dark, Light and System as soon as it is saved, without a restart, and the theme in use is painted again each time its file changes. A file that cannot be read is listed in red under the theme picker with the reason (and the line, for a syntax error); the other themes keep working, and a theme in use whose file breaks shows 1loop's dark look until it is fixed.

Under the picker:

- **New theme** writes a commented template (every key, 1loop's dark colours) and opens it in your editor.
- **Duplicate** copies the theme in use into a new file: a theme file as it is, or Dark/Light as a template.
- **Edit** opens the theme file in use.
- **Open folder** opens the themes folder.

A project can have its own theme: in the sidebar, the project's menu → **Theme…**. Its chats use that theme while they are on screen; the others keep the global one.

## Format

```toml
# SPDX-License-Identifier: MPL-2.0
name = "Nord"            # as the picker shows it; the file name when left out
base = "dark"            # dark or light: the colours left out come from it
accents = ["#88c0d0", "#a3be8c"]
roundness = 1.0          # corners: 0 square, 1 1loop's own, up to 1.6

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

Scales the corners of cards, buttons, fields, code blocks, menus and message bubbles. `0` to `1.6`; anything else is an error.

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

The agent can write a theme file in the folder above in this format, and switch to it with the settings tool: `general.theme` takes `file:<name>` for `<name>.toml`.

## matugen

[matugen](https://github.com/InioX/matugen) can write a theme from your wallpaper. Copy [`themes/matugen.toml.template`](themes/matugen.toml.template) to `~/.config/matugen/templates/1loop.toml` and add to `~/.config/matugen/config.toml`:

```toml
[templates.oneloop]
input_path = "~/.config/matugen/templates/1loop.toml"
output_path = "~/.config/1loop/themes/matugen.toml"
```

Each time matugen runs, 1loop picks the new colours up. For a light wallpaper scheme, change `base` to `"light"` in the template.

More examples: [`themes/nord.toml`](themes/nord.toml), [`themes/paper.toml`](themes/paper.toml).

## Not there yet

- Shader backgrounds (GLSL drawn behind the app, reacting to the agent's state).
- The phone takes the desktop's theme files.
