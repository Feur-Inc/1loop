package theme

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sync"

	"fyne.io/fyne/v2"
)

// 1loop (issue #476): the text fonts are stored gzipped (-1 MB in the
// binary) and inflated the first time they are used. 1loop draws with
// Inter when it is installed, so these are often never read.

// gzippedFont is a bundled font inflated on first use; the bytes are kept
// so the same slice is returned each time (the painter shares parsed faces
// by it).
type gzippedFont struct {
	name string
	gz   []byte
	once sync.Once
	data []byte
}

func (f *gzippedFont) Name() string { return f.name }

func (f *gzippedFont) Content() []byte {
	f.once.Do(func() {
		r, err := gzip.NewReader(bytes.NewReader(f.gz))
		if err != nil {
			fyne.LogError("bundled font "+f.name, err)
			return
		}
		f.data, err = io.ReadAll(r)
		if err != nil {
			fyne.LogError("bundled font "+f.name, err)
		}
	})
	return f.data
}

//go:embed font/NotoSans-Regular.ttf.gz
var notoSansRegular []byte

var regular = &gzippedFont{name: "NotoSans-Regular.ttf", gz: notoSansRegular}

//go:embed font/NotoSans-Bold.ttf.gz
var notoSansBold []byte

var bold = &gzippedFont{name: "NotoSans-Bold.ttf", gz: notoSansBold}

//go:embed font/NotoSans-Italic.ttf.gz
var notoSansItalic []byte

var italic = &gzippedFont{name: "NotoSans-Italic.ttf", gz: notoSansItalic}

//go:embed font/NotoSans-BoldItalic.ttf.gz
var notoSansBoldItalic []byte

var bolditalic = &gzippedFont{name: "NotoSans-BoldItalic.ttf", gz: notoSansBoldItalic}

//go:embed font/DejaVuSansMono-Powerline.ttf.gz
var dejaVuSansMono []byte

var monospace = &gzippedFont{name: "DejaVuSansMono-Powerline.ttf", gz: dejaVuSansMono}

//go:embed font/InterSymbols-Regular.ttf
var interSymbolsRegular []byte

var symbol = &fyne.StaticResource{
	StaticName:    "InterSymbols-Regular.ttf",
	StaticContent: interSymbolsRegular,
}
