package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"1loop/internal/core"
)

const transcriptThumbnailLimit = 64

// Display-sized images are retained instead of large decoded originals. The
// cache is bounded and used on the UI thread, including remote completions.
type transcriptThumbnailCache struct {
	images map[[32]byte]image.Image
	order  [transcriptThumbnailLimit][32]byte
	next   int
}

func (cache *transcriptThumbnailCache) get(part core.ImagePart) image.Image {
	key := sha256.Sum256([]byte(part.Data))
	if img := cache.images[key]; img != nil {
		return img
	}
	raw, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	img = thumbnail(img, 256)
	if cache.images == nil {
		cache.images = make(map[[32]byte]image.Image)
	}
	delete(cache.images, cache.order[cache.next])
	cache.order[cache.next] = key
	cache.next = (cache.next + 1) % transcriptThumbnailLimit
	cache.images[key] = img
	return img
}

type userImageView struct {
	box      *fyne.Container
	pictures *fyne.Container
}

func (ui *desktopUI) setUserImages(view *userImageView, parts []core.ImagePart) {
	var pictures []fyne.CanvasObject
	for _, part := range parts {
		if img := ui.thumbnails.get(part); img != nil {
			picture := canvas.NewImageFromImage(img)
			picture.FillMode = canvas.ImageFillContain
			picture.SetMinSize(fyne.NewSize(110, 110))
			pictures = append(pictures, surface(picture, colCode, colBorderSoft, 8, 0))
		}
	}
	view.pictures.Objects = pictures
	if len(pictures) == 0 {
		view.pictures.Hide()
	} else {
		view.pictures.Show()
	}
	view.pictures.Refresh()
}

// An arriving picture changes one row, not the text and controls of every
// message in the conversation. Off-screen rows must lose their cached height.
func (ui *desktopUI) refreshItemImages(chat *conversation, index int) {
	if chat != ui.current {
		return
	}
	if load := ui.loadingChat; load != nil {
		load.checked = min(load.checked, index)
		return
	}
	// Match the existing reloadChat behavior when a remote picture arrives.
	ui.clearSelection()
	ui.followTail = true
	switch view := ui.liveView(chat, index).(type) {
	case *userImageView:
		ui.setUserImages(view, chat.Items[index].Images)
		delete(ui.items.sizes, view.box)
	case *toolRow:
		view.update(chat.Items[index], runState(chat, index))
		ui.items.changed(view)
		ui.panel.changed(chat, index)
	default:
		ui.reloadChat(chat)
		return
	}
	ui.relayoutColumn()
}
