// SPDX-License-Identifier: MPL-2.0

package browser

// A browser lent by a paired client (issue #406): on a server with no
// Chromium of its own, Browser Use drives one that the desktop app runs on
// its computer, over the DevTools protocol the client relays (lend.go). The
// pages, input, captures and screencast are those of a local Chromium: only
// how the engine reaches the browser differs.

import (
	"context"
	"strings"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"1loop/internal/platform"
)

// RemoteHost reaches a browser another device lends: Connect returns the
// WebSocket address of its DevTools endpoint, open until ctx ends, or why
// there is none (no client lends one).
type RemoteHost interface {
	Connect(context.Context) (string, error)
}

// lends says whether this generation would use a lent browser: there is
// a host for one, and no Chromium here (ONELOOP_BROWSER=none, nothing
// installed with Install Chromium). A server with its own browser keeps
// using it, so the agent's browsing never moves to a client's computer and
// its address unless that server cannot browse at all.
func (engine *Engine) lends() bool {
	return engine.Remote != nil && Disabled()
}

// startLent connects the generation to the lent browser. Its first tab is
// the browser's own, as with a local Chromium; each session's tabs are new
// ones beside it.
func (engine *Engine) startLent() error {
	address, err := engine.Remote.Connect(engine.runCtx)
	if err != nil {
		return err
	}
	alloc, stopAlloc := chromedp.NewRemoteAllocator(engine.runCtx, address, chromedp.NoModifyURL)
	root, stopRoot := chromedp.NewContext(alloc, chromedp.WithErrorf(func(f string, a ...any) { platform.Devf("browser", f, a...) }))
	context.AfterFunc(engine.runCtx, func() { stopRoot(); stopAlloc() })
	engine.root = root
	var native struct {
		UserAgent string
		Metadata  *emulation.UserAgentMetadata
	}
	if err := chromedp.Run(root, chromedp.Evaluate(`(async()=>({userAgent:navigator.userAgent,metadata:navigator.userAgentData?await navigator.userAgentData.getHighEntropyValues(['architecture','bitness','platformVersion','fullVersionList','model','wow64']):null}))()`, &native, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		return err
	}
	engine.userAgent = strings.ReplaceAll(native.UserAgent, "HeadlessChrome/", "Chrome/")
	engine.metadata = native.Metadata
	engine.listen(root)
	return nil
}

// lentGone says whether the generation used a lent browser that is gone:
// no client lent one, or the connection to it ended. Its tabs failed or
// closed, and those the server opens again need a new connection.
func (engine *Engine) lentGone() bool {
	return engine.lent.Load() && (engine.err != nil || engine.root != nil && engine.root.Err() != nil)
}

// Lent says whether the browser in use is a lent one.
func (engine *Engine) Lent() bool {
	return engine.lent.Load()
}
