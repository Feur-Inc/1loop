// SPDX-License-Identifier: MPL-2.0

package ui

// Picture backgrounds (issue #464): a theme's background_image, a still
// picture or an animated GIF, drawn behind the window like a shader and
// covering it. A GIF plays at its own pace, and pauses like a shader: while
// the window is hidden or in the background, or animations are off.

import (
	"bytes"
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"fyne.io/fyne/v2"

	"1loop/internal/palette"
)

// gifFrames composes an animated GIF's frames into whole pictures, with
// how long each one shows.
func gifFrames(g *gif.GIF) ([]image.Image, []time.Duration) {
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	canvasImg := image.NewRGBA(bounds)
	var frames []image.Image
	var delays []time.Duration
	for i, frame := range g.Image {
		var previous *image.RGBA
		if i < len(g.Disposal) && g.Disposal[i] == gif.DisposalPrevious {
			previous = image.NewRGBA(bounds)
			copy(previous.Pix, canvasImg.Pix)
		}
		draw.Draw(canvasImg, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		shown := image.NewRGBA(bounds)
		copy(shown.Pix, canvasImg.Pix)
		frames = append(frames, shown)
		delay := 100 * time.Millisecond
		if i < len(g.Delay) && g.Delay[i] > 1 {
			delay = time.Duration(g.Delay[i]) * 10 * time.Millisecond
		}
		delays = append(delays, delay)
		if i < len(g.Disposal) {
			switch g.Disposal[i] {
			case gif.DisposalBackground:
				draw.Draw(canvasImg, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				canvasImg = previous
			}
		}
	}
	return frames, delays
}

// readPicture reads a background picture: one frame, or a GIF's frames.
func readPicture(path string) ([]image.Image, []time.Duration, error) {
	data, err := palette.ReadThemeFile(path)
	if err != nil {
		return nil, nil, err
	}
	f := bytes.NewReader(data)
	if strings.EqualFold(filepath.Ext(path), ".gif") {
		g, err := gif.DecodeAll(f)
		if err != nil {
			return nil, nil, err
		}
		frames, delays := gifFrames(g)
		return frames, delays, nil
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, nil, err
	}
	return []image.Image{img}, nil, nil
}

// syncPicture shows the active theme's picture, or hides it. Read again
// each time, so saving the picture shows it at once.
func (ui *desktopUI) syncPicture(file palette.ThemeFile, on bool) {
	b := ui.backdrop
	if b.picStop != nil {
		close(b.picStop)
		b.picStop = nil
	}
	if !on || file.Image == "" {
		b.picPath = ""
		b.picture.Image = nil
		b.picture.Hide()
		return
	}
	frames, delays, err := readPicture(file.Image)
	if err != nil || len(frames) == 0 {
		if err == nil {
			err = os.ErrInvalid
		}
		ui.shaderFailed(file.Image, err)
		return
	}
	b.picPath = file.Image
	b.picture.Image = frames[0]
	b.picture.Show()
	b.picture.Refresh()
	if len(frames) > 1 {
		b.picStop = make(chan struct{})
		go ui.playPicture(b, b.picStop, frames, delays)
	}
}

// playPicture steps through a GIF's frames, holding the frame on screen
// while the window is hidden, in the background, or animations are off.
func (ui *desktopUI) playPicture(b *backdrop, stop chan struct{}, frames []image.Image, delays []time.Duration) {
	i := 0
	for {
		select {
		case <-stop:
			return
		case <-time.After(max(delays[i], 20*time.Millisecond)):
		}
		fyne.DoAndWait(func() { // i is read again for the next delay
			if b.picStop != stop || animationsOff || !ui.inForeground || (ui.tray != nil && ui.tray.hidden) {
				return
			}
			i = (i + 1) % len(frames)
			b.picture.Image = frames[i]
			b.picture.Refresh()
		})
	}
}
