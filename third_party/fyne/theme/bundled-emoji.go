//go:build !no_emoji && !linux

package theme

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed font/EmojiOneColor.otf
var emojiFontData []byte

var emoji = &fyne.StaticResource{
	StaticName:    "EmojiOneColor.otf",
	StaticContent: emojiFontData,
}
