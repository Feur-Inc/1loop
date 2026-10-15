package agent

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"

	"1loop/internal/core"
)

// toolImageSide is the longest side a tool's screenshot keeps: models see
// no more detail above it, and local vision models pay for every pixel.
const toolImageSide = 1568

// compactToolImage makes a tool's screenshot cheaper for the model: scaled
// down to toolImageSide and sent as JPEG, as Codex does with its captures.
// Images the user attaches never come here.
func compactToolImage(part core.ImagePart) core.ImagePart {
	data, err := base64.StdEncoding.DecodeString(part.Data)
	if err != nil {
		return part
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return part
	}
	bounds := img.Bounds()
	scaled := false
	if side := max(bounds.Dx(), bounds.Dy()); side > toolImageSide {
		small := image.NewRGBA(image.Rect(0, 0, bounds.Dx()*toolImageSide/side, bounds.Dy()*toolImageSide/side))
		draw.CatmullRom.Scale(small, small.Bounds(), img, bounds, draw.Src, nil)
		img, scaled = small, true
	}
	// JPEG suits photos and most screens; a flat screen can stay smaller as
	// PNG. Keep whichever is lighter.
	best := core.ImagePart{}
	var out bytes.Buffer
	if jpeg.Encode(&out, img, &jpeg.Options{Quality: 80}) == nil {
		best = core.ImagePart{Mime: "image/jpeg", Data: base64.StdEncoding.EncodeToString(out.Bytes())}
	}
	if scaled {
		out.Reset()
		if png.Encode(&out, img) == nil && (best.Data == "" || base64.StdEncoding.EncodedLen(out.Len()) < len(best.Data)) {
			best = core.ImagePart{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(out.Bytes())}
		}
	}
	if best.Data == "" || (!scaled && len(best.Data) >= len(part.Data)) {
		return part
	}
	return best
}
