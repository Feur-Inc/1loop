// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"1loop/assets"

	"fyne.io/fyne/v2/test"
)

// The ring's amber takes the accent; with the default accent it is unchanged.
func TestAccentedArtwork(t *testing.T) {
	data, _ := assets.Artwork.ReadFile("1loop-ring.png")
	art, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	saved := colAmber
	defer func() { colAmber = saved }()
	colAmber = color.NRGBA{0xFF, 0xB0, 0x00, 0xFF}
	same := accentedArtwork(art)
	colAmber = color.NRGBA{0xFF, 0x4F, 0xA3, 0xFF}
	pink := accentedArtwork(art)
	amber := 0
	for i := 0; i < len(same.Pix); i += 4 {
		if same.Pix[i+3] == 0xFF && same.Pix[i] == 0xFF && same.Pix[i+1] == 0xB0 && same.Pix[i+2] == 0 {
			amber++
			if pink.Pix[i+1] != 0x4F || pink.Pix[i+2] != 0xA3 {
				t.Fatalf("amber pixel %d not recoloured: %v", i/4, pink.Pix[i:i+4])
			}
		}
	}
	if amber == 0 {
		t.Fatal("no amber pixel in the ring artwork")
	}
}

// A ring made after the accent is set, as the welcome screen's is at
// startup with a saved accent, is drawn in that accent at once, not in the
// brand's amber of the raw artwork (#399).
func TestLoopMarkStartsInAccent(t *testing.T) {
	test.NewTempApp(t)
	saved := colAmber
	defer func() { colAmber, brandRingInk = saved, "" }()
	colAmber = color.NRGBA{0x2F, 0xC2, 0xA4, 0xFF} // teal
	assertAccentRing(t, newLoopMark(60).image.Image)
}

// An intro stopped before its end (a running chat opened over the welcome
// screen) is drawn again in a new accent, not left in the old one.
func TestStoppedIntroTakesNewAccent(t *testing.T) {
	test.NewTempApp(t)
	saved := colAmber
	defer func() { colAmber, brandRingInk = saved, "" }()
	colAmber = color.NRGBA{0xA8, 0x7B, 0xFF, 0xFF} // purple
	mark := newLoopMark(60)
	mark.showBrand()     // a frame of the intro, in purple
	mark.state = "intro" // as intro leaves it once stopped midway
	mark.stop()
	colAmber = color.NRGBA{0x2F, 0xC2, 0xA4, 0xFF} // teal
	mark.recolour()
	assertAccentRing(t, mark.image.Image)
}

// assertAccentRing checks the ring is drawn in teal, with no amber or
// purple left in it.
func assertAccentRing(t *testing.T, img image.Image) {
	t.Helper()
	if img == nil {
		t.Fatal("the ring shows the raw artwork, in the brand's amber, not the accent")
	}
	teal, other := 0, 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A != 0xFF || max(c.R, c.G, c.B)-min(c.R, c.G, c.B) < 0x40 { // transparent, white or grey
				continue
			}
			if c.G > c.R && c.B > c.R && c.G > c.B {
				teal++
			} else {
				other++
			}
		}
	}
	if teal == 0 || other > 0 {
		t.Fatalf("ring pixels: %d in the accent, %d in another colour", teal, other)
	}
}
