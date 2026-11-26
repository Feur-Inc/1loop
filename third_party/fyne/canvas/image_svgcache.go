// SPDX-License-Identifier: MPL-2.0

package canvas

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"sync"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/cache"
	"fyne.io/fyne/v2/internal/svg"
)

// 1loop: an SVG resource is colourised and parsed once, not at every
// Refresh of every image showing it. Rows and chips show the same few icons
// hundreds of times; each Refresh used to run the XML colouriser and the SVG
// parser again (docs/perf/2026-11-audit.md, hotspot 5).

const svgDecodedLimit = 4096

var (
	svgDecoded   = map[string]*svg.Decoder{}
	svgDecodedMu sync.Mutex
	svgDrawMu    sync.Mutex // a shared decoder keeps its target while drawing
)

// svgKey names what an image's SVG resource looks like: the resource, and
// for a themed one the colour it is painted in; for a plain one its bytes.
func (i *Image) svgKey() (string, bool) {
	if i.Resource == nil || !svg.IsFileSVG(i.Resource.Name()) {
		return "", false
	}
	if res, ok := i.Resource.(fyne.ThemedResource); ok {
		th := cache.WidgetTheme(i)
		if th == nil {
			return "", false
		}
		col := th.Color(res.ThemeColorName(), fyne.CurrentApp().Settings().ThemeVariant())
		r, g, b, a := col.RGBA()
		return fmt.Sprintf("%s|%04x%04x%04x%04x", i.Resource.Name(), r, g, b, a), true
	}
	content := i.Resource.Content()
	if len(content) == 0 {
		return "", false
	}
	return fmt.Sprintf("%s|%p|%d", i.Resource.Name(), unsafe.Pointer(&content[0]), len(content)), true
}

// refreshCachedSVG is Refresh for an SVG resource, its decoder shared.
// It reports false when the image is something else.
func (i *Image) refreshCachedSVG() bool {
	key, ok := i.svgKey()
	if !ok {
		return false
	}
	svgDecodedMu.Lock()
	decoder := svgDecoded[key]
	svgDecodedMu.Unlock()
	if decoder == nil {
		rc, err := i.updateReader()
		if err != nil || rc == nil {
			return false
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return false
		}
		decoder, err = svg.NewDecoder(bytes.NewReader(content))
		if err != nil {
			return false
		}
		svgDecodedMu.Lock()
		if len(svgDecoded) >= svgDecodedLimit {
			svgDecoded = map[string]*svg.Decoder{}
		}
		svgDecoded[key] = decoder
		svgDecodedMu.Unlock()
	}
	i.isSVG = true
	i.icon = decoder
	config := decoder.Config()
	i.aspect = config.Aspect
	if i.FillMode == ImageFillOriginal {
		i.SetMinSize(scaleSize(i, config.Width, config.Height))
	}
	size := i.Size()
	if size.Width != 0 && size.Height != 0 {
		tex, err := i.renderSVG(size.Width, size.Height)
		if err != nil {
			fyne.LogError("Failed to render SVG", err)
			return true
		}
		i.Image = tex
	}
	i.previousRender = true
	Refresh(i)
	return true
}

// svgSizedName is the name an SVG's rasterised copy of w × h pixels is
// cached under.
func svgSizedName(name string, w, h int) string {
	return name + "@" + strconv.Itoa(w) + "x" + strconv.Itoa(h)
}
