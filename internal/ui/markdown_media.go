// SPDX-License-Identifier: MPL-2.0

package ui

// Images and videos in an issue's or a pull request's text, as GitHub shows
// them (#442). Only the side panel's GitHub preview draws them: in a chat,
// an image an agent writes stays a link, so that nothing it writes is loaded
// without a click. An image shows its alt text, as a link, while it loads
// and if it cannot; then the picture, as wide as it is, at most the panel's
// width. A video, which Fyne cannot play, is a card that opens it in the
// browser. Pictures load in the background, a few at a time, and are kept
// for the next preview.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	_ "golang.org/x/image/webp"

	"1loop/internal/githubconnector"
	"1loop/internal/platform"
)

// mdMedia is what a Markdown view needs to draw its images: relayout, once
// a picture has loaded or changed height, lays its page out again (its
// scroll), as Fyne only measures a container's objects when it is.
type mdMedia struct {
	relayout func()
	pending  bool
}

// changed asks for one relayout, at the next turn of the loop, however
// many pictures changed in this one.
func (media *mdMedia) changed() {
	if media.relayout == nil || media.pending {
		return
	}
	media.pending = true
	remoteImages.deliver(func() {
		media.pending = false
		media.relayout()
	})
}

// paragraph lays a paragraph out with its images and videos on lines of
// their own, as GitHub does, the text between them wrapped as usual.
func (media *mdMedia) paragraph(spans []span, size float32, ink color.Color) fyne.CanvasObject {
	if address, ok := bareVideo(spans); ok {
		return newVideoCard(address)
	}
	var parts []fyne.CanvasObject
	var run []span
	flush := func() {
		for len(run) > 0 && (run[0].hard || strings.TrimSpace(run[0].text) == "" && run[0].math == "") {
			run = run[1:]
		}
		for len(run) > 0 && (run[len(run)-1].hard || strings.TrimSpace(run[len(run)-1].text) == "" && run[len(run)-1].math == "") {
			run = run[:len(run)-1]
		}
		if len(run) > 0 {
			parts = append(parts, newFlowText(run, size, ink))
		}
		run = nil
	}
	for _, s := range spans {
		if s.media == "" {
			run = append(run, s)
			continue
		}
		flush()
		parts = append(parts, media.object(s, size))
	}
	flush()
	switch len(parts) {
	case 0:
		return newFlowText(nil, size, ink)
	case 1:
		return parts[0]
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(8), parts...)
}

// object is one image or video.
func (media *mdMedia) object(s span, size float32) fyne.CanvasObject {
	if s.video || videoAddress(s.media) {
		return newVideoCard(s.media)
	}
	return newRemoteImage(s, size, media)
}

// hasMedia says some span is an image or a video.
func hasMedia(spans []span) bool {
	for _, s := range spans {
		if s.media != "" {
			return true
		}
	}
	return false
}

// bareVideo is a paragraph that is only a video's address. GitHub writes a
// video dropped into an issue as its user-attachments address alone on its
// line (an image gets ![…](…) or <img>), and plays it.
func bareVideo(spans []span) (string, bool) {
	if len(spans) != 1 || spans[0].link == "" || spans[0].media != "" {
		return "", false
	}
	address := spans[0].link
	if strings.TrimSpace(spans[0].text) != address {
		return "", false // a link with its own label
	}
	if attachmentAddress(address) || videoAddress(address) {
		return address, true
	}
	return "", false
}

// attachmentAddress is a file attached on GitHub, by its address.
func attachmentAddress(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "github.com" && strings.HasPrefix(parsed.Path, "/user-attachments/assets/")
}

// videoAddress is an https address of a video file, by its extension.
func videoAddress(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	switch strings.ToLower(path.Ext(parsed.Path)) {
	case ".mp4", ".mov", ".webm", ".m4v":
		return true
	}
	return false
}

// openInBrowser opens an image or a video where the system opens links.
func openInBrowser(address string) {
	target, err := url.Parse(address)
	if err != nil || target.Scheme != "https" && target.Scheme != "http" {
		return
	}
	if err := fyne.CurrentApp().OpenURL(target); err != nil {
		platform.Devf("ui", "could not open %s: %v", address, err)
	}
}

// ---------------------------------------------------------------------------
// Images.

// remoteImage is an image from the web: its alt text, a link, until the
// picture has loaded, then the picture, left-aligned, as wide as it is (or
// as its tag asks) but no wider than the panel.
type remoteImage struct {
	widget.BaseWidget
	src     string
	asked   float32 // the width its tag asks for, 0 for its own
	media   *mdMedia
	alt     *flowText
	picture *canvas.Image
	natural fyne.Size // the picture's size in pixels, once loaded
	width   float32   // last laid out width
	shown   float32   // the height last given to the layout
	loaded  func()    // told when the picture has loaded, before the page is laid out again
}

func newRemoteImage(s span, size float32, media *mdMedia) *remoteImage {
	label := s
	label.media, label.video, label.mediaWidth = "", false, 0
	view := &remoteImage{src: s.media, asked: s.mediaWidth, media: media, alt: newFlowText([]span{label}, size, colText)}
	view.picture = canvas.NewImageFromImage(nil)
	view.picture.FillMode = canvas.ImageFillStretch
	view.picture.ScaleMode = canvas.ImageScaleSmooth
	view.picture.Hide()
	view.ExtendBaseWidget(view)
	loadRemoteImage(s.media, func(img image.Image) {
		view.show(img)
	})
	return view
}

// show puts the loaded picture in place of the alt text.
func (view *remoteImage) show(img image.Image) {
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		return
	}
	view.natural = fyne.NewSize(float32(bounds.Dx()), float32(bounds.Dy()))
	view.picture.Image = img
	view.picture.Show()
	view.alt.Hide()
	view.Refresh()
	if view.loaded != nil {
		view.loaded()
	}
	view.media.changed()
}

// heightAt is its height in a column width wide: the picture's once loaded,
// its alt text's until then.
func (view *remoteImage) heightAt(width float32) float32 {
	if view.picture.Image != nil {
		return view.pictureSize(width).Height
	}
	view.alt.Resize(fyne.NewSize(width, 10))
	return view.alt.MinSize().Height
}

// pictureSize is the picture's size in a column width wide.
func (view *remoteImage) pictureSize(width float32) fyne.Size {
	return fitPicture(view.natural, view.asked, width)
}

// fitPicture scales a picture of natural pixels to the width its tag asks
// for, if any, and at most to width, keeping its proportions.
func fitPicture(natural fyne.Size, asked, width float32) fyne.Size {
	if natural.Width <= 0 || natural.Height <= 0 {
		return fyne.Size{}
	}
	w := natural.Width
	if asked > 0 {
		w = asked
	}
	if width > 0 {
		w = fyne.Min(w, width)
	}
	return fyne.NewSize(w, w*natural.Height/natural.Width)
}

func (view *remoteImage) Tapped(*fyne.PointEvent) { openInBrowser(view.src) }

func (view *remoteImage) Cursor() desktop.Cursor { return desktop.PointerCursor }

func (view *remoteImage) CreateRenderer() fyne.WidgetRenderer {
	return &remoteImageRenderer{view: view}
}

type remoteImageRenderer struct{ view *remoteImage }

// MinSize: the picture's height at the last width laid out, like wrapped
// text; before any layout, at a column of 600.
func (renderer *remoteImageRenderer) MinSize() fyne.Size {
	view := renderer.view
	if view.picture.Image == nil {
		return view.alt.MinSize()
	}
	width := view.width
	if width == 0 {
		width = 600
	}
	return fyne.NewSize(40, view.heightAt(width))
}

func (renderer *remoteImageRenderer) Layout(size fyne.Size) {
	view := renderer.view
	view.width = size.Width
	view.alt.Move(fyne.Position{})
	view.alt.Resize(size)
	if view.picture.Image == nil {
		return
	}
	fit := view.pictureSize(size.Width)
	view.picture.Move(fyne.Position{})
	view.picture.Resize(fit)
	if fit.Height != view.shown {
		// The column changed width, so the picture's height did: the page
		// must place what is below it again.
		view.shown = fit.Height
		if fit.Height != size.Height {
			view.media.changed()
		}
	}
}

func (renderer *remoteImageRenderer) Refresh() {
	renderer.view.alt.Refresh()
	renderer.view.picture.Refresh()
}

func (renderer *remoteImageRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.view.alt, renderer.view.picture}
}

func (renderer *remoteImageRenderer) Destroy() {}

// ---------------------------------------------------------------------------
// Videos.

// newVideoCard is a video: a card as wide as the column, with ▶ and the
// file's name, that opens it in the browser.
func newVideoCard(address string) fyne.CanvasObject {
	name := "Video"
	if parsed, err := url.Parse(address); err == nil {
		if base := path.Base(parsed.Path); base != "" && base != "/" && base != "." && !attachmentAddress(address) {
			name = base
		}
	}
	play := canvas.NewImageFromResource(tinted(theme.MediaPlayIcon(), colText))
	play.SetMinSize(fyne.NewSquareSize(28))
	disc := canvas.NewCircle(colRaised)
	disc.StrokeColor, disc.StrokeWidth = colBorder, 1
	button := container.NewStack(container.NewGridWrap(fyne.NewSquareSize(44), disc), container.NewCenter(play))
	title := textLine(name, 13.5, colText)
	title.TextStyle.Bold = true
	text := container.NewVBox(layout.NewSpacer(), title, textLine("Opens in your browser", 12, colMuted), layout.NewSpacer())
	card := surface(container.NewBorder(nil, nil, padded(0, 0, 0, 12, button), nil, text), colCode, colBorderSoft, 10, 14)
	return container.NewStack(card, newTapArea(func() { openInBrowser(address) }))
}

// ---------------------------------------------------------------------------
// Loading.

const (
	remoteImageLimit   = 64        // pictures kept for the next preview
	remoteImageMemory  = 128 << 20 // at most this much of them decoded, the newest always kept
	remoteImageBytes   = 25 << 20  // the largest file read
	remoteImageSide    = 2400      // the longest side a picture is kept at
	remoteImageLoaders = 4         // pictures downloaded at once
)

// remoteImages holds the pictures loaded, and the ones loading with who
// waits for them, by address. A failure is not kept: the next preview
// (Refresh) tries again.
var remoteImages = struct {
	sync.Mutex
	done    map[string]image.Image
	order   []string // done's addresses, oldest first
	waiting map[string][]func(image.Image)
	slots   chan struct{}
	fetch   func(ctx context.Context, address string) (image.Image, error)
	deliver func(func()) // runs on the UI thread: fyne.Do
}{
	done:    map[string]image.Image{},
	waiting: map[string][]func(image.Image){},
	slots:   make(chan struct{}, remoteImageLoaders),
	fetch:   fetchRemoteImage,
	deliver: fyne.Do,
}

// loadRemoteImage calls show, on the UI thread, once the picture at address
// has loaded: at once when it already has. It is never called when the
// picture cannot load (the alt text stays).
func loadRemoteImage(address string, show func(image.Image)) {
	if parsed, err := url.Parse(address); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return // http, data: and relative addresses stay links
	}
	cache := &remoteImages
	cache.Lock()
	if img := cache.done[address]; img != nil {
		cache.Unlock()
		show(img)
		return
	}
	waiters, loading := cache.waiting[address]
	cache.waiting[address] = append(waiters, show)
	fetch, deliver := cache.fetch, cache.deliver
	cache.Unlock()
	if loading {
		return
	}
	go func() {
		cache.slots <- struct{}{}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		img, err := fetch(ctx, address)
		cancel()
		<-cache.slots
		cache.Lock()
		waiters := cache.waiting[address]
		delete(cache.waiting, address)
		if err == nil {
			cache.done[address] = img
			cache.order = append(cache.order, address)
			// 64 screenshots of 2400 px would hold more than a gigabyte.
			kept := 0
			for _, address := range cache.order {
				kept += pictureBytes(cache.done[address])
			}
			for len(cache.order) > 1 && (len(cache.order) > remoteImageLimit || kept > remoteImageMemory) {
				kept -= pictureBytes(cache.done[cache.order[0]])
				delete(cache.done, cache.order[0])
				cache.order = cache.order[1:]
			}
		}
		cache.Unlock()
		if err != nil {
			platform.Devf("ui", "image not loaded: %s: %v", address, err)
			return
		}
		deliver(func() {
			for _, show := range waiters {
				show(img)
			}
		})
	}()
}

// pictureBytes is about what a decoded picture holds in memory.
func pictureBytes(img image.Image) int {
	if img == nil {
		return 0
	}
	return img.Bounds().Dx() * img.Bounds().Dy() * 4
}

// imageClient follows redirects, as a browser does (GitHub's attachments
// answer with one to a signed address), but only to https.
var imageClient = &http.Client{
	CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if request.URL.Scheme != "https" {
			return errors.New("redirected away from https")
		}
		return nil
	},
}

// fetchRemoteImage downloads and decodes a picture, kept at most
// remoteImageSide on its longest side. A file attached on GitHub is read
// with the user's GitHub sign-in, as github.com shows it to them: some are
// only served to an account that can see the repository.
func fetchRemoteImage(ctx context.Context, address string) (image.Image, error) {
	if githubconnector.IsAttachment(address) {
		data, kind, err := githubconnector.Default.Download(ctx, address, remoteImageBytes)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(kind, "image/") {
			return nil, fmt.Errorf("not an image: %s", kind)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return thumbnail(img, remoteImageSide), nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp,image/*;q=0.8")
	response, err := imageClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if kind := response.Header.Get("Content-Type"); kind != "" && !strings.HasPrefix(kind, "image/") && !strings.HasPrefix(kind, "application/octet-stream") {
		return nil, fmt.Errorf("not an image: %s", kind)
	}
	img, _, err := image.Decode(io.LimitReader(response.Body, remoteImageBytes))
	if err != nil {
		return nil, err
	}
	return thumbnail(img, remoteImageSide), nil
}
