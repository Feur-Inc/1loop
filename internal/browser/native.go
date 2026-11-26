// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chromedp/cdproto/target"

	"1loop/internal/core"
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
		return fmt.Errorf("This browser opens http, https and about:blank addresses only")
	}
	return nil
}
