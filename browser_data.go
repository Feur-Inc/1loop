package main

// The agent's browser profile: where it is kept, and how it is cleared.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// browserProfileDir is the browser's profile, beside 1loop's state (so a
// copy of 1loop run with another config folder has a browser of its own).
func browserProfileDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "1loop", "browser")
}

// clearBrowserData erases what the browser keeps: cookies and logins,
// cache, site storage, history. A browser not running loses its profile at
// once. A running one loses its cookies, cache and site data now, through
// Chrome, and the whole profile when it next starts.
func (ui *desktopUI) clearBrowserData(done func(error)) {
	dir := browserProfileDir()
	if dir == "" {
		done(nil)
		return
	}
	var ctx context.Context
	if engine := ui.browser; engine != nil {
		engine.pages.Range(func(_, value any) bool {
			if page := value.(*browserPage); page.ctx != nil && page.life.Err() == nil {
				ctx = page.ctx
				return false
			}
			return true
		})
	}
	if ctx == nil {
		var err error
		for attempt := 0; attempt < 5; attempt++ { // a Chrome just closed may still be writing
			if err = os.RemoveAll(dir); err == nil {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		devf("browser", "browser data cleared (not running): %v", err)
		done(err)
		return
	}
	os.WriteFile(dir+".clear", nil, 0600)
	go func() {
		run, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		err := chromedp.Run(run, chromedp.ActionFunc(func(ctx context.Context) error {
			origins := map[string]bool{}
			if cookies, err := storage.GetCookies().Do(ctx); err == nil {
				for _, cookie := range cookies {
					domain := strings.TrimPrefix(cookie.Domain, ".")
					origins["https://"+domain], origins["http://"+domain] = true, true
				}
			}
			if err := network.ClearBrowserCookies().Do(ctx); err != nil {
				return err
			}
			if err := network.ClearBrowserCache().Do(ctx); err != nil {
				return err
			}
			for origin := range origins {
				_ = storage.ClearDataForOrigin(origin, "all").Do(ctx)
			}
			return nil
		}))
		devf("browser", "browser data cleared (running; the profile goes at the next start): %v", err)
		fyne.Do(func() { done(err) })
	}()
}
