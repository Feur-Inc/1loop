package ui

import (
	"bytes"
	"encoding/base64"
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"1loop/internal/agent"
)

type generatedImagePreview struct {
	box      *fyne.Container
	progress *widget.ProgressBarInfinite
	open     func()
}

func newGeneratedImagePreview(item agent.ChatItem, state string, open func()) *generatedImagePreview {
	preview := &generatedImagePreview{box: container.NewVBox(), progress: widget.NewProgressBarInfinite(), open: open}
	preview.update(item, state)
	return preview
}

func (preview *generatedImagePreview) update(item agent.ChatItem, state string) {
	preview.progress.Stop()
	preview.box.Objects = nil
	if state == "running" || state == "pending" {
		preview.box.Add(surface(padded(18, 18, 18, 18, container.NewVBox(textLine("Creating your image…", 14, colMuted), preview.progress)), colCode, colBorderSoft, 12, 0))
		preview.progress.Start()
	} else if len(item.Images) > 0 {
		raw, err := base64.StdEncoding.DecodeString(item.Images[0].Data)
		if err == nil {
			if img, _, err := image.Decode(bytes.NewReader(raw)); err == nil {
				picture := canvas.NewImageFromImage(img)
				picture.FillMode = canvas.ImageFillContain
				picture.SetMinSize(fyne.NewSize(280, 280))
				preview.box.Add(surface(picture, colCode, colBorderSoft, 12, 0))
				preview.box.Add(newRow("Saved in generated-images · Open details", iconFolder, styleChip, preview.open))
			}
		}
	}
	preview.box.Refresh()
}
