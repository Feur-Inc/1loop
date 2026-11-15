// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

const shot = "https://github.com/user-attachments/assets/641bc892-cc8f-463f-931c-183ffe202f16"

// Images are spans of their own, from Markdown and from GitHub's <img>
// and <video> tags; any other tag is still shown as written.
func TestMarkdownMediaSpans(t *testing.T) {
	for _, tc := range []struct {
		markdown string
		want     []span
	}{
		{"![Broken layout](" + shot + ")", []span{{text: "Broken layout", link: shot, media: shot}}},
		{"![](" + shot + ")", []span{{text: shot, link: shot, media: shot}}},
		{"see ![a](https://x.example/a.png) here", []span{{text: "see "}, {text: "a", link: "https://x.example/a.png", media: "https://x.example/a.png"}, {text: " here"}}},
		{`<img width="300" alt="A &amp; B" src="` + shot + `" />`, []span{{text: "A & B", link: shot, media: shot, mediaWidth: 300}}},
		{`text <img src='https://x.example/b.png'> end`, []span{{text: "text "}, {text: "https://x.example/b.png", link: "https://x.example/b.png", media: "https://x.example/b.png"}, {text: " end"}}},
		{`<video src="https://x.example/v.mp4" controls>`, []span{{text: "https://x.example/v.mp4", link: "https://x.example/v.mp4", media: "https://x.example/v.mp4", video: true}}},
		{`a <kbd>b</kbd>`, []span{{text: "a <kbd>b</kbd>"}}},
	} {
		var got []span
		if tc.markdown[0] == '<' { // an HTML block
			got = htmlSpans(tc.markdown)
		} else {
			got = paragraphSpans(t, tc.markdown)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%q: got %+v, want %+v", tc.markdown, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q span %d: got %+v, want %+v", tc.markdown, i, got[i], tc.want[i])
			}
		}
	}
}

// A video dropped into an issue is its address alone on its line.
func TestBareVideo(t *testing.T) {
	for _, tc := range []struct {
		markdown string
		video    bool
	}{
		{shot, true},
		{"https://x.example/demo.MP4", true},
		{"[the video](" + shot + ")", false}, // a link with a label stays one
		{"see " + shot, false},
		{"https://x.example/page", false},
		{"http://x.example/demo.mp4", false},
	} {
		_, got := bareVideo(paragraphSpans(t, tc.markdown))
		if got != tc.video {
			t.Errorf("%q: video %v, want %v", tc.markdown, got, tc.video)
		}
	}
}

func TestFitPicture(t *testing.T) {
	for _, tc := range []struct {
		natural       fyne.Size
		asked, column float32
		want          fyne.Size
	}{
		{fyne.NewSize(1536, 864), 0, 512, fyne.NewSize(512, 288)},      // scaled to the column
		{fyne.NewSize(200, 100), 0, 512, fyne.NewSize(200, 100)},       // never enlarged
		{fyne.NewSize(1536, 864), 300, 512, fyne.NewSize(300, 168.75)}, // the width its tag asks for
		{fyne.NewSize(1536, 864), 900, 512, fyne.NewSize(512, 288)},    // but no wider than the column
		{fyne.Size{}, 0, 512, fyne.Size{}},
	} {
		if got := fitPicture(tc.natural, tc.asked, tc.column); got != tc.want {
			t.Errorf("fitPicture(%v, %v, %v) = %v, want %v", tc.natural, tc.asked, tc.column, got, tc.want)
		}
	}
}

// withFetch replaces how pictures are downloaded, and empties the cache.
func withFetch(t *testing.T, fetch func(context.Context, string) (image.Image, error)) {
	t.Helper()
	cache := &remoteImages
	cache.Lock()
	saved, savedDeliver := cache.fetch, cache.deliver
	cache.fetch, cache.done, cache.order, cache.waiting = fetch, map[string]image.Image{}, nil, map[string][]func(image.Image){}
	cache.deliver = func(f func()) { uiQueue <- f }
	cache.Unlock()
	t.Cleanup(func() {
		cache.Lock()
		cache.fetch, cache.deliver, cache.done, cache.order = saved, savedDeliver, map[string]image.Image{}, nil
		cache.Unlock()
	})
}

// uiQueue holds what the loaders hand to the UI thread: the test's.
var uiQueue = make(chan func(), 64)

// pump runs what the loaders handed to the UI thread.
func pump() {
	for {
		select {
		case f := <-uiQueue:
			f()
		default:
			return
		}
	}
}

// waitFor runs the UI loop's queued calls until ok, or fails.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for pump(); !ok(); pump() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The preview shows the alt text, then the picture in its place, at the
// column's width; one download serves every view of the same address, and
// the next preview has it at once.
func TestRemoteImageLoads(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	release := make(chan struct{})
	var downloads atomic.Int32
	withFetch(t, func(ctx context.Context, address string) (image.Image, error) {
		downloads.Add(1)
		<-release
		return image.NewNRGBA(image.Rect(0, 0, 1536, 864)), nil
	})
	var relayouts atomic.Int32
	media := &mdMedia{relayout: func() { relayouts.Add(1) }}
	view := newMarkdownMediaView("Before\n\n![Broken layout]("+shot+")\n\n![again]("+shot+")", media)
	view.box.Resize(fyne.NewSize(512, 2000))
	var images []*remoteImage
	for _, object := range view.box.Objects {
		if img, ok := object.(*remoteImage); ok {
			images = append(images, img)
		}
	}
	if len(images) != 2 {
		t.Fatalf("got %d images, want 2: %#v", len(images), view.box.Objects)
	}
	first := images[0]
	test.WidgetRenderer(first) // its renderer, as the window would make it
	if !first.alt.Visible() || first.picture.Visible() {
		t.Fatal("while loading, the alt text shows, not the picture")
	}
	if got := first.alt.spans[0].text; got != "Broken layout" {
		t.Errorf("alt text %q", got)
	}
	close(release)
	waitFor(t, "the picture", func() bool { return first.picture.Image != nil && images[1].picture.Image != nil })
	if downloads.Load() != 1 {
		t.Errorf("%d downloads, want 1", downloads.Load())
	}
	if first.alt.Visible() || !first.picture.Visible() {
		t.Error("once loaded, the picture shows instead of the alt text")
	}
	first.Resize(fyne.NewSize(512, first.MinSize().Height))
	if got := first.picture.Size(); got != fyne.NewSize(512, 288) {
		t.Errorf("picture laid out at %v, want 512 x 288", got)
	}
	if got := first.MinSize().Height; got != 288 {
		t.Errorf("min height %v, want 288", got)
	}
	waitFor(t, "a relayout", func() bool { return relayouts.Load() > 0 })

	again := newRemoteImage(span{text: "x", link: shot, media: shot}, 14, media)
	if again.picture.Image == nil {
		t.Error("a picture already loaded shows at once")
	}
}

// A picture that cannot load leaves its alt text, a link; the next
// preview tries again.
func TestRemoteImageFails(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	var downloads atomic.Int32
	withFetch(t, func(context.Context, string) (image.Image, error) {
		downloads.Add(1)
		return nil, context.DeadlineExceeded
	})
	media := &mdMedia{}
	view := newRemoteImage(span{text: "alt", link: shot, media: shot}, 14, media)
	waitFor(t, "the download", func() bool { return downloads.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	pump()
	if view.picture.Image != nil || !view.alt.Visible() {
		t.Error("a failed picture leaves its alt text")
	}
	test.WidgetRenderer(view).Layout(fyne.NewSize(300, 40))
	if got := view.alt.linkAt(fyne.NewPos(4, 8)); got != shot {
		t.Errorf("the alt text links to %q, want the image", got)
	}
	newRemoteImage(span{text: "alt", link: shot, media: shot}, 14, media)
	waitFor(t, "a second try", func() bool { return downloads.Load() == 2 })
	// Only https is loaded.
	newRemoteImage(span{text: "alt", link: "http://x.example/a.png", media: "http://x.example/a.png"}, 14, media)
	time.Sleep(20 * time.Millisecond)
	if downloads.Load() != 2 {
		t.Error("an http image was downloaded")
	}
}

// Downloads follow GitHub's redirect to the signed address, and stop at a
// file that is not an image.
func TestFetchRemoteImage(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/user-attachments/assets/a":
			http.Redirect(w, r, "/signed/a.png?X-Amz-Signature=x", http.StatusFound)
		case "/signed/a.png":
			w.Header().Set("Content-Type", "image/png")
			png.Encode(w, image.NewNRGBA(image.Rect(0, 0, 3000, 1500)))
		default:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>"))
		}
	}))
	defer server.Close()
	saved := imageClient.Transport
	imageClient.Transport = server.Client().Transport
	defer func() { imageClient.Transport = saved }()

	img, err := fetchRemoteImage(context.Background(), server.URL+"/user-attachments/assets/a")
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Size(); got != image.Pt(remoteImageSide, remoteImageSide/2) {
		t.Errorf("kept at %v, want %d x %d", got, remoteImageSide, remoteImageSide/2)
	}
	if _, err := fetchRemoteImage(context.Background(), server.URL+"/page"); err == nil {
		t.Error("a page was taken for an image")
	}
}

// In a chat, an image stays a link: nothing an agent writes is loaded
// without a click.
func TestChatImagesStayLinks(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	withFetch(t, func(context.Context, string) (image.Image, error) {
		t.Error("a chat image was downloaded")
		return nil, context.Canceled
	})
	view := newMarkdownView("![x](" + shot + ")\n\n" + shot)
	for _, object := range view.box.Objects {
		if _, ok := object.(*flowText); !ok {
			t.Errorf("chat block %T, want text", object)
		}
	}
	time.Sleep(20 * time.Millisecond)
}

// A paragraph's images go on lines of their own, between its text; a bare
// video address is a card.
func TestMediaParagraph(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	withFetch(t, func(context.Context, string) (image.Image, error) { return nil, context.Canceled })
	media := &mdMedia{}
	box, ok := media.paragraph(paragraphSpans(t, "Before ![a](https://x.example/a.png) after"), 14, colText).(*fyne.Container)
	if !ok || len(box.Objects) != 3 {
		t.Fatalf("got %#v, want text, image, text", box)
	}
	if _, ok := box.Objects[1].(*remoteImage); !ok {
		t.Errorf("middle %T, want the image", box.Objects[1])
	}
	if _, ok := media.paragraph(paragraphSpans(t, shot), 14, colText).(*fyne.Container); !ok {
		t.Error("a bare attachment is a video card")
	}
	if _, ok := media.paragraph(paragraphSpans(t, "just text"), 14, colText).(*flowText); !ok {
		t.Error("text alone stays text")
	}
}

// GitHub's before/after tables: a cell's image shows in it, under its
// text, at the column's width, and the row grows once it has loaded.
func TestTableImages(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(loopTheme{theme.DefaultTheme()})
	withFetch(t, func(context.Context, string) (image.Image, error) {
		return image.NewNRGBA(image.Rect(0, 0, 1000, 500)), nil
	})
	media := &mdMedia{}
	view := newMarkdownMediaView("| | Before |\n| - | - |\n| First | ![Before]("+shot+") |", media)
	table, ok := view.box.Objects[0].(*markdownTable)
	if !ok {
		t.Fatalf("got %T, want a table", view.box.Objects[0])
	}
	picture := table.pictures[1][1]
	if len(picture) != 1 || len(table.cells[1][1].spans) != 0 {
		t.Fatalf("the cell holds %d pictures and text %+v", len(picture), table.cells[1][1].spans)
	}
	renderer := test.WidgetRenderer(table)
	before := renderer.MinSize().Height
	waitFor(t, "the picture", func() bool { return picture[0].picture.Image != nil })
	table.Resize(fyne.NewSize(500, renderer.MinSize().Height))
	if after := renderer.MinSize().Height; after <= before+100 {
		t.Errorf("row height %v once loaded, was %v: the picture is not in it", after, before)
	}
	if got := picture[0].Size(); got.Width <= 100 || got.Height != got.Width/2 {
		t.Errorf("picture laid out at %v", got)
	}
}

// The cache keeps at most remoteImageMemory of decoded pictures, the newest
// always: big screenshots must not pile up in memory.
func TestRemoteImageCacheMemory(t *testing.T) {
	big := image.NewNRGBA(image.Rect(0, 0, 2400, 2400)) // 23 MB decoded
	withFetch(t, func(context.Context, string) (image.Image, error) { return big, nil })
	shown := 0
	for i := 0; i < 12; i++ {
		loadRemoteImage(fmt.Sprintf("https://example.com/%d.png", i), func(image.Image) { shown++ })
		want := i + 1
		waitFor(t, "the picture", func() bool { return shown == want })
	}
	cache := &remoteImages
	cache.Lock()
	defer cache.Unlock()
	kept := 0
	for _, address := range cache.order {
		kept += pictureBytes(cache.done[address])
	}
	if kept > remoteImageMemory || len(cache.done) != len(cache.order) {
		t.Fatalf("%d pictures, %d bytes kept: more than %d", len(cache.order), kept, remoteImageMemory)
	}
	if cache.done["https://example.com/11.png"] == nil {
		t.Fatal("the newest picture was dropped")
	}
}
