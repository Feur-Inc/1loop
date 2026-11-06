// SPDX-License-Identifier: MPL-2.0

package core

import (
	"bytes"
	"image"
)

// MaxImagePixels bounds the images 1loop decodes: decoding takes four bytes
// a pixel whatever the file's size, so a small file of a huge image (a
// 50 megapixel one: 200 MB) could exhaust the memory.
const MaxImagePixels = 50_000_000

// ImageDecodable tells, from its header only, that raw is an image of a
// registered format within MaxImagePixels.
func ImageDecodable(raw []byte) bool {
	config, _, err := image.DecodeConfig(bytes.NewReader(raw))
	return err == nil && config.Width > 0 && config.Height > 0 && config.Width*config.Height <= MaxImagePixels
}
