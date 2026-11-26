//go:build no_emoji || linux

// 1loop (issue #476): Linux builds draw emoji with the system's colour emoji
// font (Noto Color Emoji and the like, see painter.systemEmoji) instead of
// bundling EmojiOneColor (4.2 MB).

package theme

import "fyne.io/fyne/v2"

var emoji fyne.Resource
