// SPDX-License-Identifier: MPL-2.0

//go:build linux && !(wayland && cgo)

package ui

// Images from the clipboard in the X11 build. Fyne's clipboard (GLFW's) is
// text-only; golang.design/x/clipboard speaks the X11 selection protocol in
// Go (or Wayland's data-control, when the compositor offers it) over a
// connection of its own. The Wayland build keeps its own reader
// (clipboard_wayland.go): it works on every compositor, data-control or not.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"golang.design/x/clipboard"

	"1loop/internal/platform"
)

// clipboardReady has nothing to start: the clipboard is asked on each paste.
func clipboardReady() {}

func imageClipboardData() ([]byte, error) {
	data, err := libraryClipboardImage()
	if err == nil || errors.Is(err, errNoClipboardImage) {
		return data, err
	}
	if _, lookErr := exec.LookPath("xclip"); lookErr == nil {
		platform.Devf("clipboard", "%v, trying xclip", err)
		return externalImageClipboardData()
	}
	return nil, err
}

// libraryClipboardImage reads the clipboard's image: PNG when offered, else
// the first other image type, as the copying application sent it.
func libraryClipboardImage() ([]byte, error) {
	if err := clipboard.Init(); err != nil {
		return nil, fmt.Errorf("the clipboard is not available: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
	defer cancel()
	formats, err := clipboard.Formats(ctx)
	if err != nil {
		return nil, err
	}
	var candidates []clipboard.Format
	for _, format := range formats {
		if format == clipboard.FmtImage {
			candidates = append([]clipboard.Format{format}, candidates...)
		} else if strings.HasPrefix(format.MIME(), "image/") {
			candidates = append(candidates, format)
		}
	}
	for _, format := range candidates {
		data, err := clipboard.Read(ctx, format)
		switch {
		case errors.Is(err, clipboard.ErrNoData):
			continue
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("the application that copied the image did not send it")
		case err != nil:
			return nil, err
		case len(data) > clipboardImageLimit:
			return nil, fmt.Errorf("the image in the clipboard is too large")
		}
		return data, nil
	}
	return nil, errNoClipboardImage
}
