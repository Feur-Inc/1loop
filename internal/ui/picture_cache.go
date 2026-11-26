// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"container/list"
	"context"
	"image"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	xdraw "golang.org/x/image/draw"
)

// catalogPictureBytes bounds the decoded pictures of the extension catalog
// kept in memory (#525): the least recently drawn go first.
const catalogPictureBytes = 24 << 20

// catalogPictureScale keeps a picture at twice the size it is drawn at, for
// a high-density screen; catalogPictureSide is the size of one with no size.
const (
	catalogPictureScale = 2
	catalogPictureSide  = 1000
)

// catalogPictureDownload caps what is read of a picture before decoding it.
const catalogPictureDownload = 8 << 20

type cachedPicture struct {
	address string
	img     image.Image
	bytes   int
}

// pictureCache keeps decoded pictures up to catalogPictureBytes, and gives
// each address one download at a time: the views waiting for a picture are
// filled when its download ends.
type pictureCache struct {
	mu      sync.Mutex
	order   *list.List // *cachedPicture, the most recently used at the front
	items   map[string]*list.Element
	bytes   int
	waiting map[string][]*canvas.Image
}

// catalogPictures holds the pictures of the extension catalog.
var catalogPictures = newPictureCache()

func newPictureCache() *pictureCache {
	return &pictureCache{order: list.New(), items: map[string]*list.Element{}, waiting: map[string][]*canvas.Image{}}
}

// get returns the picture of address, marking it as used, or nil.
func (cache *pictureCache) get(address string) image.Image {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	element, ok := cache.items[address]
	if !ok {
		return nil
	}
	cache.order.MoveToFront(element)
	return element.Value.(*cachedPicture).img
}

// put keeps a picture, dropping the least recently used ones over the
// bound. A picture larger than the bound is not kept at all.
func (cache *pictureCache) put(address string, img image.Image) {
	size := imageBytes(img)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if old, ok := cache.items[address]; ok {
		cache.bytes -= old.Value.(*cachedPicture).bytes
		cache.order.Remove(old)
		delete(cache.items, address)
	}
	if size > catalogPictureBytes {
		return
	}
	cache.items[address] = cache.order.PushFront(&cachedPicture{address: address, img: img, bytes: size})
	cache.bytes += size
	for cache.bytes > catalogPictureBytes {
		old := cache.order.Remove(cache.order.Back()).(*cachedPicture)
		delete(cache.items, old.address)
		cache.bytes -= old.bytes
	}
}

// join queues view to be filled with the picture of address. It reports
// whether the caller must start the download: none is under way yet.
func (cache *pictureCache) join(address string, view *canvas.Image) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	views, busy := cache.waiting[address]
	cache.waiting[address] = append(views, view)
	return !busy
}

// finish ends the download of address and returns the views waiting for it.
func (cache *pictureCache) finish(address string) []*canvas.Image {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	views := cache.waiting[address]
	delete(cache.waiting, address)
	return views
}

// imageBytes is the memory a decoded picture takes.
func imageBytes(img image.Image) int {
	bounds := img.Bounds()
	return bounds.Dx() * bounds.Dy() * 4
}

// pictureAllowed tells whether a picture's address may be fetched: https only.
func pictureAllowed(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

// shrinkCatalogPicture scales a decoded picture down to at most catalogPictureScale
// times the size it is drawn at, so that a huge one takes no more memory
// than it is shown with.
func shrinkCatalogPicture(img image.Image, size fyne.Size) image.Image {
	limitW, limitH := int(size.Width*catalogPictureScale), int(size.Height*catalogPictureScale)
	if limitW <= 0 || limitH <= 0 {
		limitW, limitH = catalogPictureSide, catalogPictureSide
	}
	bounds := img.Bounds()
	if bounds.Dx() <= limitW && bounds.Dy() <= limitH {
		return img
	}
	scale := min(float64(limitW)/float64(bounds.Dx()), float64(limitH)/float64(bounds.Dy()))
	w := max(1, int(float64(bounds.Dx())*scale))
	h := max(1, int(float64(bounds.Dy())*scale))
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(out, out.Bounds(), img, bounds, xdraw.Src, nil)
	return out
}

// downloadPicture fetches and decodes one picture, fitted to size; nil when
// it cannot be had.
func downloadPicture(address string, size fyne.Size) image.Image {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	decoded, _, err := image.Decode(io.LimitReader(response.Body, catalogPictureDownload))
	if err != nil {
		return nil
	}
	return shrinkCatalogPicture(decoded, size)
}

// fetchPicture downloads address into the cache and fills the views that
// waited for it, on the interface's thread.
func fetchPicture(address string, size fyne.Size) {
	decoded := downloadPicture(address, size)
	if decoded != nil {
		catalogPictures.put(address, decoded)
	}
	views := catalogPictures.finish(address)
	fyne.Do(func() {
		if decoded == nil {
			return
		}
		for _, view := range views {
			view.Image = decoded
			view.Refresh()
		}
	})
}
