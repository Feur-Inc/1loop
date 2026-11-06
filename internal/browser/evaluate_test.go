// SPDX-License-Identifier: MPL-2.0

package browser

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestEvaluate(t *testing.T) {
	path := ""
	// Google Chrome first: on Ubuntu, chromium-browser may be the stub that
	// installs the snap, which never starts here.
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
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
	// The first Run starts the browser: its context bounds the whole test.
	ctx, cancel = context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		// This tests evaluation, not the machine: a browser that cannot
		// start here (no display, no sandbox) is no reason to fail.
		t.Skipf("%s does not start here: %v", path, err)
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
		`const o = {a: 1}; o.self = o; o`:        `{"a":1,"self":"[Circular]"}`,
		`const s = {x: 1}; [s, s]`:               `[{"x":1},{"x":1}]`,
		`document.createElement("b")`:            `"<b></b>"`,
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

// A rejection keeps its reason when it isn't an Error.
func TestJavaScriptError(t *testing.T) {
	for _, c := range []struct {
		exception runtime.ExceptionDetails
		want      string
	}{
		{runtime.ExceptionDetails{Text: "Uncaught (in promise)", Exception: &runtime.RemoteObject{Type: "string", Value: []byte(`"why"`)}}, `JavaScript: Uncaught (in promise): "why"`},
		{runtime.ExceptionDetails{Text: "Uncaught (in promise) Error: boom", Exception: &runtime.RemoteObject{Description: "Error: boom\n    at <anonymous>:1:22"}}, "JavaScript: Uncaught (in promise) Error: boom: at <anonymous>:1:22"},
		{runtime.ExceptionDetails{Text: "Uncaught", Exception: &runtime.RemoteObject{Description: "ReferenceError: nope is not defined"}}, "JavaScript: Uncaught: ReferenceError: nope is not defined"},
		{runtime.ExceptionDetails{Text: "Uncaught (in promise)", Exception: &runtime.RemoteObject{Type: "undefined"}}, "JavaScript: Uncaught (in promise): undefined"},
	} {
		if got := javaScriptError(&c.exception).Error(); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
}
