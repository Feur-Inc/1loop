// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"bytes"
	"image/color"
	"image/png"
	"testing"

	"1loop/assets"
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
