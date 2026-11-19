// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"net/url"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"1loop/internal/gitrepo"
)

// links records where links go: the browser or the side panel.
func links(t *testing.T) (browser, panel *[]string) {
	browser, panel = &[]string{}, &[]string{}
	savedURL, savedReference := openURL, openReference
	t.Cleanup(func() { openURL, openReference = savedURL, savedReference })
	openURL = func(target *url.URL) error { *browser = append(*browser, target.String()); return nil }
	openReference = func(link string) { *panel = append(*panel, link) }
	return browser, panel
}

// tapRowNamed finds a button by its text.
func tapRowNamed(object fyne.CanvasObject, text string) *tapRow {
	switch object := object.(type) {
	case *tapRow:
		if object.text == text {
			return object
		}
	case *fyne.Container:
		for _, child := range object.Objects {
			if row := tapRowNamed(child, text); row != nil {
				return row
			}
		}
	}
	return nil
}

// "Open on GitHub" opens the browser; it showed the issue in the side
// panel again (#441). A link to an issue in text still opens the panel.
func TestOpenOnGitHubOpensTheBrowser(t *testing.T) {
	test.NewTempApp(t)
	browser, panel := links(t)
	address := "https://github.com/Feur-Inc/1loop/issues/441"
	ref, _ := referenceOf(address)
	view := referenceView(&gitrepo.Detail{Item: gitrepo.Item{Ref: ref, URL: address, Title: "Open on GitHub", State: "open", Created: time.Now()}}, func() {})
	open := tapRowNamed(view, "Open on GitHub")
	if open == nil {
		t.Fatal("no Open on GitHub button")
	}
	open.onTap()
	if len(*browser) != 1 || (*browser)[0] != address || len(*panel) != 0 {
		t.Errorf("Open on GitHub: browser %q, side panel %q", *browser, *panel)
	}

	*browser = (*browser)[:0]
	openLink(address)
	if len(*panel) != 1 || len(*browser) != 0 {
		t.Errorf("link in text: browser %q, side panel %q", *browser, *panel)
	}
}
