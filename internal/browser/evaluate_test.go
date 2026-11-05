// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestEvaluate(t *testing.T) {
	path := ""
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if found, err := exec.LookPath(name); err == nil {
			path = found
			break
		}
	}
	if path == "" {
		t.Skip("no Chromium to run the page in")
	}
	alloc, cancel := chromedp.NewExecAllocator(context.Background(), append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(path), chromedp.NoSandbox)...)
	defer cancel()
	ctx, cancel := chromedp.NewContext(alloc)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatal(err)
	}
	for expression, want := range map[string]string{
		`42`:                                     `42`,
		`Promise.resolve(42)`:                    `42`,
		`(async () => 42)()`:                     `42`,
		`await Promise.resolve(42)`:              `42`,
		`({a: 1, b: [2, "x"]})`:                  `{"a":1,"b":[2,"x"]}`,
		`Promise.resolve({n: NaN, i: Infinity})`: `{"n":"NaN","i":"Infinity"}`,
		`NaN`:                                    `"NaN"`,
		`-Infinity`:                              `"-Infinity"`,
		`42n`:                                    `"42n"`,
		`Promise.resolve(10n)`:                   `"10n"`,
		`undefined`:                              `null`,
		`(async () => {})()`:                     `null`,
		`document.title = "t"; document.title`:   `"t"`,
	} {
		var got []byte
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) (err error) {
			got, err = evaluate(ctx, expression)
			return err
		}))
		if err != nil || string(got) != want {
			t.Errorf("%s = %s, %v; want %s", expression, got, err, want)
		}
	}
	for _, expression := range []string{`(async () => { throw new Error("boom") })()`, `Promise.reject(new Error("boom"))`, `nope()`} {
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := evaluate(ctx, expression)
			return err
		}))
		if err == nil || !strings.Contains(err.Error(), "JavaScript") {
			t.Errorf("%s: error %v, want a JavaScript error", expression, err)
		}
	}
}
