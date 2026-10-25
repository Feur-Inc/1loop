package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"

	"1loop/assets"
)

// iconSizes are the hicolor sizes the Linux packages install.
var iconSizes = []int{16, 24, 32, 48, 64, 128, 256, 512}

// icons writes the launcher icon at every hicolor size, in
// DIR/hicolor/NxN/apps/NAME.png, for the Linux packages.
func icons(args []string) {
	if len(args) < 1 || len(args) > 2 {
		fail("usage: 1loop-release icons DIR [NAME]")
	}
	name := "1loop"
	if len(args) == 2 {
		name = args[1]
	}
	ring, err := png.Decode(bytes.NewReader(must(assets.Artwork.ReadFile("1loop-ring.png"))))
	if err != nil {
		fail("Cannot read the ring artwork: " + err.Error())
	}
	for _, size := range iconSizes {
		var out bytes.Buffer
		if err := png.Encode(&out, launcherIcon(ring, size)); err != nil {
			fail(err.Error())
		}
		path := filepath.Join(args[0], "hicolor", fmt.Sprintf("%dx%d", size, size), "apps", name+".png")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			fail(err.Error())
		}
		if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
			fail(err.Error())
		}
	}
}

// launcherIcon is the ring on a black rounded square, with the margin the
// desktop's icon grid expects (the bare artwork fills its square edge to
// edge and looks like a tile among the other applications). The proportions
// are those of the 512 px design.
func launcherIcon(ring image.Image, size int) image.Image {
	scale := float64(size) / 512
	margin, radius := 34*scale, 104*scale
	icon := image.NewNRGBA(image.Rect(0, 0, size, size))
	last := float64(size) - 1
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			// Distance past the rounded square's corner arc, for a smooth rim.
			fx, fy := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(math.Max(margin+radius-fx, fx-(last+1-margin-radius)), 0)
			dy := math.Max(math.Max(margin+radius-fy, fy-(last+1-margin-radius)), 0)
			edge := math.Min(math.Min(fx-margin, last+1-margin-fx), math.Min(fy-margin, last+1-margin-fy))
			cover := math.Min(math.Min(1, math.Max(0, edge+0.5)), math.Max(0, radius+0.5-math.Hypot(dx, dy)))
			if cover > 0 {
				icon.SetNRGBA(x, y, color.NRGBA{A: uint8(math.Min(1, cover) * 255)})
			}
		}
	}
	side := int(math.Round(330 * scale))
	at := (size - side) / 2
	scaled := image.NewNRGBA(image.Rect(0, 0, side, side))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), ring, ring.Bounds(), xdraw.Src, nil)
	draw.Draw(icon, image.Rect(at, at, at+side, at+side), scaled, image.Point{}, draw.Over)
	return icon
}

func must(data []byte, err error) []byte {
	if err != nil {
		fail(err.Error())
	}
	return data
}
