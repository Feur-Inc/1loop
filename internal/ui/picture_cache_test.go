// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// The catalog's pictures stay within their bound: the least recently drawn
// go first (#525).
func TestCatalogPicturesStayWithinTheirBound(t *testing.T) {
	cache := newPictureCache()
	const side = 1000 // 4 MB each
	for i := range 10 {
		cache.put(string(rune('a'+i)), image.NewNRGBA(image.Rect(0, 0, side, side)))
	}
	if cache.bytes > catalogPictureBytes {
		t.Fatalf("%d bytes kept, the bound is %d", cache.bytes, catalogPictureBytes)
	}
	if cache.get("a") != nil {
		t.Fatal("the oldest picture is still kept")
	}
	if cache.get("j") == nil {
		t.Fatal("the newest picture is not kept")
	}
}

// Drawing a picture makes it the last to go.
func TestCatalogPictureDrawnRecentlyIsKept(t *testing.T) {
	cache := newPictureCache()
	const side = 1000
	cache.put("old", image.NewNRGBA(image.Rect(0, 0, side, side)))
	for i := range 5 {
		cache.put(string(rune('a'+i)), image.NewNRGBA(image.Rect(0, 0, side, side)))
	}
	cache.get("old")
	cache.put("z", image.NewNRGBA(image.Rect(0, 0, side, side)))
	if cache.get("old") == nil {
		t.Fatal("a picture drawn recently was dropped before the others")
	}
	if cache.get("a") != nil {
		t.Fatal("the least recently drawn picture is still kept")
	}
}

// A picture larger than the whole bound is not kept, so it cannot push out
// the rest.
func TestCatalogPictureOverTheBoundIsNotKept(t *testing.T) {
	cache := newPictureCache()
	cache.put("small", image.NewNRGBA(image.Rect(0, 0, 100, 100)))
	cache.put("huge", image.NewNRGBA(image.Rect(0, 0, 3000, 3000))) // 36 MB
	if cache.get("huge") != nil {
		t.Fatal("a picture over the bound is kept")
	}
	if cache.get("small") == nil {
		t.Fatal("a picture over the bound pushed out the others")
	}
}

// Only https pictures are fetched.
func TestCatalogPicturesAreHTTPSOnly(t *testing.T) {
	for address, want := range map[string]bool{
		"https://example.com/social.png": true,
		"http://example.com/social.png":  false,
		"file:///etc/passwd":             false,
		"javascript:alert(1)":            false,
		"https:///no-host.png":           false,
		"":                               false,
	} {
		if got := pictureAllowed(address); got != want {
			t.Errorf("pictureAllowed(%q) = %v, want %v", address, got, want)
		}
	}
}

// A picture is scaled to at most twice the size it is drawn at.
func TestCatalogPictureIsShrunkToItsSize(t *testing.T) {
	huge := image.NewNRGBA(image.Rect(0, 0, 4000, 2000))
	out := shrinkCatalogPicture(huge, fyne.NewSize(100, 50))
	if w := out.Bounds().Dx(); w > 200 {
		t.Fatalf("width %d, at most 200 for a 100-point view", w)
	}
	if h := out.Bounds().Dy(); h > 100 {
		t.Fatalf("height %d, at most 100 for a 50-point view", h)
	}
	if small := image.NewNRGBA(image.Rect(0, 0, 50, 50)); shrinkCatalogPicture(small, fyne.NewSize(100, 50)) != image.Image(small) {
		t.Fatal("a picture already smaller than the view was scaled")
	}
}

// One download runs per address: the views asking while it runs are filled
// by it, and a later one starts again.
func TestCatalogPictureDownloadsOnce(t *testing.T) {
	cache := newPictureCache()
	first, second := canvas.NewImageFromImage(nil), canvas.NewImageFromImage(nil)
	if !cache.join("https://example.com/a.png", first) {
		t.Fatal("the first view must start the download")
	}
	if cache.join("https://example.com/a.png", second) {
		t.Fatal("a second download of the same picture started")
	}
	views := cache.finish("https://example.com/a.png")
	if len(views) != 2 {
		t.Fatalf("%d views waiting, want 2", len(views))
	}
	if !cache.join("https://example.com/a.png", canvas.NewImageFromImage(nil)) {
		t.Fatal("a download after the first one ended did not start")
	}
}
