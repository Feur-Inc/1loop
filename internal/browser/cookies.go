package browser

import (
	"context"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"

	"1loop/internal/platform"
)

// ImportCookies adds cookies to the agent's browser profile: through the
// running browser, or a browser started on the profile for this alone. It
// says how many were kept; Chrome refuses the ones it finds invalid.
func (engine *Engine) ImportCookies(ctx context.Context, cookies []*network.CookieParam) (int, error) {
	// A temporary reference keeps the shared browser alive while tabs open
	// or close. The normal idle shutdown flushes the profile after import.
	engine.refs.Add(1)
	defer func() {
		if engine.refs.Add(-1) == 0 {
			engine.stopIfIdle()
		}
	}()
	if err := engine.start(); err != nil {
		return 0, err
	}
	engine.mu.Lock()
	root := engine.root
	engine.mu.Unlock()
	run, cancel := context.WithTimeout(root, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return setCookies(run, cookies)
}

// setCookies sets them a batch at a time; a batch Chrome refuses is set one
// by one, so one bad cookie does not lose the rest.
func setCookies(ctx context.Context, cookies []*network.CookieParam) (int, error) {
	kept := 0
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		for start := 0; start < len(cookies); start += 200 {
			batch := cookies[start:min(start+200, len(cookies))]
			if storage.SetCookies(batch).Do(ctx) == nil {
				kept += len(batch)
				continue
			}
			for _, cookie := range batch {
				if err := storage.SetCookies([]*network.CookieParam{cookie}).Do(ctx); err == nil {
					kept++
				} else if ctx.Err() != nil {
					return ctx.Err()
				}
			}
		}
		return nil
	}))
	platform.Devf("browser", "imported %d of %d cookies: %v", kept, len(cookies), err)
	return kept, err
}
