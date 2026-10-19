package browser

import (
	"context"
	"fmt"
	"net/url"

	"1loop/internal/core"
	"github.com/chromedp/cdproto/target"
)

// NativeHost owns Android views; CDP still handles page content and input.
type NativeHost interface {
	Connect(context.Context) (string, target.ID, error)
	Open(context.Context, Info) (target.ID, error)
	Resize(context.Context, Info) error
	Capture(context.Context, string, bool) (core.ImagePart, error)
	Close(context.Context, string) error
	Stop(context.Context) error
}

func nativeURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && raw != "about:blank" {
		return fmt.Errorf("Local browser allows http, https and about:blank URLs only")
	}
	return nil
}
