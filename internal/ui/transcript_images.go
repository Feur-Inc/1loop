// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"image"

	"fyne.io/fyne/v2"

	"1loop/internal/core"
)

const transcriptThumbnailLimit = 64

// Display-sized images are retained instead of large decoded originals. The
// cache is bounded and used on the UI thread, including remote completions.
type transcriptThumbnailCache struct {
	images map[[32]byte]cachedThumbnail
	order  [transcriptThumbnailLimit][32]byte
	next   int
}

// cachedThumbnail is a picture made small, with the size of the original.
type cachedThumbnail struct {
	img           image.Image
	width, height int
}

// getSized is the thumbnail of part and the size of the full picture.
func (cache *transcriptThumbnailCache) getSized(part core.ImagePart) (image.Image, int, int) {
	key := sha256.Sum256([]byte(part.Data))
	if cached, ok := cache.images[key]; ok {
		return cached.img, cached.width, cached.height
	}
	raw, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil || !core.ImageDecodable(raw) {
		return nil, 0, 0
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, 0, 0
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	img = thumbnail(img, 256)
	if cache.images == nil {
		cache.images = make(map[[32]byte]cachedThumbnail)
	}
	delete(cache.images, cache.order[cache.next])
	cache.order[cache.next] = key
	cache.next = (cache.next + 1) % transcriptThumbnailLimit
	cache.images[key] = cachedThumbnail{img, width, height}
	return img, width, height
}

type userImageView struct {
	box      *fyne.Container
	pictures *fyne.Container
}

func (ui *desktopUI) setUserImages(view *userImageView, parts []core.ImagePart) {
	var pictures []fyne.CanvasObject
	for _, part := range parts {
		if img, w, h := ui.thumbnails.getSized(part); img != nil {
			// Its own shape, within 240 × 160, never larger than the
			// original (the thumbnail may be smaller than that).
			size := fitPictureBox(w, h, messagePictureWidth, messagePictureHeight)
			pictures = append(pictures, newPictureTile(img, size, func() { ui.openPicture("Image", part) }, nil))
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
	ui = ui.viewOf(chat) // the window showing it (windows.go)
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
		ui.items.changed(view.box)
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
