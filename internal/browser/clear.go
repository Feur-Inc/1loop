package browser

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"

	"1loop/internal/platform"
)

// ClearData erases what the agent's browser keeps: cookies and logins,
// cache, site storage, history. A browser not running loses its profile at
// once. A running one loses its cookies, cache and site data now, through
// Chrome, and the whole profile when it next starts.
func ClearData(engine *Engine) error {
	dir := ProfileDir()
	if dir == "" {
		return nil
	}
	var ctx context.Context
	if engine != nil {
		engine.Pages.Range(func(_, value any) bool {
			if page := value.(*Page); page.Ctx != nil && page.Life.Err() == nil {
				ctx = page.Ctx
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
		platform.Devf("browser", "browser data cleared (not running): %v", err)
		return err
	}
	os.WriteFile(dir+".clear", nil, 0600)
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
	platform.Devf("browser", "browser data cleared (running; the profile goes at the next start): %v", err)
	return err
}
