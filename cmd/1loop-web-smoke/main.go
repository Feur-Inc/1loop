// SPDX-License-Identifier: MPL-2.0

// The browser client's smoke run (issue #267): a fake streaming model, a
// 1loop-server serving the web build at /app/, and headless Chromium
// loading it, with screenshots and load timings in -out.
//
//	go run ./cmd/1loop-web-smoke -server bin/1loop-server -web bin/web -out .out/web
//
// Canvas positions (Fyne draws everything in one <canvas>) are flags, read
// off an earlier run's screenshots.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

var (
	serverBin = flag.String("server", "bin/1loop-server", "1loop-server binary")
	webDir    = flag.String("web", "bin/web", "web build folder")
	outDir    = flag.String("out", ".out/web", "screenshots and logs")
	chat      = flag.String("chat", "", "x,y of a chat in the sidebar, to open it")
	prompt    = flag.String("prompt", "", "x,y of the composer, to type and send there")
	scroll    = flag.String("scroll", "", "x,y over the conversation, to scroll it")
	connect   = flag.String("connect", "", "x,y of the pairing sheet's Connect button")
	copyAt    = flag.String("copy", "", "x,y of a Copy button, to read the clipboard after")
	longChat  = flag.Int("long", 60, "turns in the long chat")
)

const listen = "127.0.0.1:47167"

func main() {
	flag.Parse()
	must(os.MkdirAll(*outDir, 0o755))
	data, err := os.MkdirTemp(*outDir, "server-")
	must(err)
	workspace := filepath.Join(data, "project")
	must(os.MkdirAll(workspace, 0o755))
	model := fakeModel()

	// The server, its console on a pipe for pairing.
	logFile, err := os.Create(filepath.Join(*outDir, "server.log"))
	must(err)
	server := exec.Command(*serverBin, "-listen", listen, "-data-dir", filepath.Join(data, "state"), "-workspace", workspace,
		"-provider", "compatible", "-base-url", "http://"+model+"/v1", "-model", "fake", "-web-dir", *webDir)
	console, err := server.StdinPipe()
	must(err)
	server.Stdout, server.Stderr = logFile, logFile
	server.Env = append(os.Environ(), "HOME="+data, "XDG_CONFIG_HOME="+filepath.Join(data, "config"), "ONELOOP_UPDATE_URL=http://127.0.0.1:9/none")
	must(server.Start())
	defer server.Process.Kill()
	base := "https://" + listen
	insecure := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	waitFor(func() bool { r, err := insecure.Get(base + "/api/info"); return err == nil && r.Body.Close() == nil })
	var consoleMu sync.Mutex
	approve := func(pairing, code string) {
		consoleMu.Lock()
		defer consoleMu.Unlock()
		fmt.Fprintf(console, "pair %s %s\n", pairing, code)
		time.Sleep(300 * time.Millisecond)
	}

	// Seed: pair as a script, then chats, one of them long.
	var pair struct{ Pairing, Code string }
	call(insecure, base, "", "POST", "/api/pair", map[string]string{"device": "smoke seed"}, &pair)
	approve(pair.Pairing, pair.Code)
	var paired struct{ Token string }
	call(insecure, base, "", "POST", "/api/pair/confirm", map[string]string{"pairing": pair.Pairing, "code": pair.Code}, &paired)
	token := paired.Token
	seed := func(chat, text string) string {
		var reply struct{ Chat string }
		call(insecure, base, token, "POST", "/api/send", map[string]any{"chat": chat, "text": text, "mode": "", "workspace": workspace}, &reply)
		waitFor(func() bool {
			var state struct {
				Chats []struct {
					ID      string
					Running bool
				}
			}
			call(insecure, base, token, "GET", "/api/state", nil, &state)
			for _, c := range state.Chats {
				if c.ID == reply.Chat {
					return !c.Running
				}
			}
			return false
		})
		return reply.Chat
	}
	seed("", "Plan the move of the docs site to Hugo")
	seed("", "Why is the build cache so slow on CI?")
	long := seed("", "A long chat: turn 1")
	for i := 2; i <= *longChat; i++ {
		seed(long, fmt.Sprintf("A long chat: turn %d", i))
	}
	log.Printf("seeded, token %d chars", len(token))

	// The certificate warning, as a browser without an exception sees it.
	func() {
		ctx, cancel := browser(false)
		defer cancel()
		chromedp.Run(ctx, chromedp.Navigate(base+"/app/"))
		time.Sleep(2 * time.Second)
		shot(ctx, "01-certificate-warning")
	}()

	// A fresh browser: load, pairing.
	ctx, cancel := browser(true)
	defer cancel()
	var consoleLines []string
	pairBodies := make(chan network.RequestID, 4)
	var streamMu sync.Mutex
	events := 0
	chromedp.ListenTarget(ctx, func(ev any) {
		switch e := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			var parts []string
			for _, a := range e.Args {
				parts = append(parts, string(a.Value))
			}
			streamMu.Lock()
			consoleLines = append(consoleLines, strings.Join(parts, " "))
			streamMu.Unlock()
		case *network.EventResponseReceived:
			if strings.HasSuffix(e.Response.URL, "/api/pair") {
				pairBodies <- e.RequestID
			}
			if strings.HasSuffix(e.Response.URL, "/api/events") {
				streamMu.Lock()
				events++
				streamMu.Unlock()
			}
		}
	})
	start := time.Now()
	must(chromedp.Run(ctx, network.Enable(), chromedp.Navigate(base+"/app/")))
	var loaded float64
	waitFor(func() bool {
		chromedp.Run(ctx, chromedp.Evaluate(`window.loopTimings && window.loopTimings.instantiated || 0`, &loaded))
		return loaded > 0
	})
	log.Printf("wasm instantiated %.0f ms after navigation start (harness: %s)", loaded, time.Since(start).Round(time.Millisecond))
	var resources string
	chromedp.Run(ctx, chromedp.Evaluate(`JSON.stringify(performance.getEntriesByType("resource").map(e => ({name: e.name.split("/").pop(), transfer: e.transferSize, decoded: e.decodedBodySize, ms: Math.round(e.duration)})))`, &resources))
	log.Printf("resources: %s", resources)
	select {
	case id := <-pairBodies:
		var body []byte
		chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) (err error) {
			body, err = network.GetResponseBody(id).Do(ctx)
			return err
		}))
		var p struct{ Pairing, Code string }
		json.Unmarshal(body, &p)
		log.Printf("page asked to pair: %s", p.Pairing)
		time.Sleep(1500 * time.Millisecond)
		shot(ctx, "02-pairing")
		approve(p.Pairing, p.Code)
	case <-time.After(30 * time.Second):
		log.Printf("the page never asked to pair")
		shot(ctx, "02-no-pairing")
	}
	firstFrame := time.Since(start)
	if *connect != "" {
		click(ctx, *connect)
		time.Sleep(3 * time.Second)
		shot(ctx, "03-connected-by-pairing")
	} else { // the token from the seed pairing, as a saved one
		js := fmt.Sprintf(`localStorage.setItem("1loop.remote-server", JSON.stringify({url: location.origin, token: %q}))`, token)
		must(chromedp.Run(ctx, chromedp.Evaluate(js, nil), chromedp.Reload()))
		waitFor(func() bool {
			chromedp.Run(ctx, chromedp.Evaluate(`window.loopTimings && window.loopTimings.instantiated || 0`, &loaded))
			return loaded > 0
		})
		time.Sleep(4 * time.Second)
		shot(ctx, "03-chats")
	}
	if *chat != "" {
		click(ctx, *chat)
		time.Sleep(3 * time.Second)
		shot(ctx, "04-long-chat")
	}
	if *scroll != "" {
		x, y := xy(*scroll)
		chromedp.Run(ctx, chromedp.MouseEvent(input.MouseMoved, x, y))
		t := time.Now()
		for i := 0; i < 10; i++ {
			chromedp.Run(ctx, chromedp.MouseEvent(input.MouseWheel, x, y, func(p *input.DispatchMouseEventParams) *input.DispatchMouseEventParams {
				return p.WithDeltaX(0).WithDeltaY(-240)
			}))
			time.Sleep(50 * time.Millisecond)
		}
		log.Printf("10 wheel steps sent in %s", time.Since(t).Round(time.Millisecond))
		time.Sleep(time.Second)
		shot(ctx, "05-scrolled")
	}
	if *prompt != "" {
		click(ctx, *prompt)
		chromedp.Run(ctx, chromedp.KeyEvent("Please answer slowly: é à ü — ünïcödé ✓"))
		time.Sleep(time.Second)
		shot(ctx, "06-typed")
		chromedp.Run(ctx, chromedp.KeyEvent(kb.Enter))
		streamMu.Lock()
		before := events
		streamMu.Unlock()
		for i, wait := range []time.Duration{3, 3, 12} {
			time.Sleep(wait * time.Second)
			shot(ctx, fmt.Sprintf("07-streaming-%d", i+1))
		}
		streamMu.Lock()
		log.Printf("/api/events responses during the run: %d (before %d)", events, before)
		streamMu.Unlock()
	}
	if *copyAt != "" {
		chromedp.Run(ctx, browserGrant())
		click(ctx, *copyAt)
		time.Sleep(time.Second)
		var text string
		err := chromedp.Run(ctx, chromedp.Evaluate(`navigator.clipboard.readText()`, &text, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		log.Printf("clipboard after Copy: %q (%v)", text, err)
	}
	streamMu.Lock()
	os.WriteFile(filepath.Join(*outDir, "console.log"), []byte(strings.Join(consoleLines, "\n")), 0o644)
	streamMu.Unlock()
	log.Printf("first pairing request %s after navigation", firstFrame.Round(time.Millisecond))
}

// browserGrant lets the page read and write the clipboard without a prompt.
func browserGrant() chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		return cdpbrowser.GrantPermissions([]cdpbrowser.PermissionType{cdpbrowser.PermissionTypeClipboardReadWrite, cdpbrowser.PermissionTypeClipboardSanitizedWrite}).Do(ctx)
	})
}

func browser(trust bool) (context.Context, context.CancelFunc) {
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(1280, 800),
		chromedp.Flag("use-angle", "swiftshader"),
		chromedp.Flag("enable-unsafe-swiftshader", true),
		chromedp.Flag("ignore-certificate-errors", trust),
	)
	if path := os.Getenv("CHROME"); path != "" {
		options = append(options, chromedp.ExecPath(path))
	}
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), options...)
	ctx, cancel := chromedp.NewContext(alloc)
	return ctx, func() { cancel(); cancelAlloc() }
}

func shot(ctx context.Context, name string) {
	var png []byte
	if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&png)); err != nil {
		log.Printf("screenshot %s: %v", name, err)
		return
	}
	os.WriteFile(filepath.Join(*outDir, name+".png"), png, 0o644)
	log.Printf("shot %s", name)
}

func click(ctx context.Context, at string) {
	x, y := xy(at)
	// glfw-js takes the click's position from the last mousemove, which a
	// real pointer always sends first.
	chromedp.Run(ctx, chromedp.MouseEvent(input.MouseMoved, x-3, y-3), chromedp.MouseEvent(input.MouseMoved, x, y))
	time.Sleep(150 * time.Millisecond)
	if err := chromedp.Run(ctx, chromedp.MouseClickXY(x, y)); err != nil {
		log.Printf("click %s: %v", at, err)
	}
}

func xy(at string) (float64, float64) {
	x, y, _ := strings.Cut(at, ",")
	fx, _ := strconv.ParseFloat(x, 64)
	fy, _ := strconv.ParseFloat(y, 64)
	return fx, fy
}

// fakeModel answers like an OpenAI-compatible API, streaming word by word:
// slowly when asked to, for watching a run arrive in the browser.
func fakeModel() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	answer := strings.Repeat("Here is a **markdown** answer with `code`, a list:\n\n- one point\n- another point\n\n", 4) +
		"```go\nfunc main() {\n\tfmt.Println(\"hello from the fake model\")\n}\n```\n"
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"data":[{"id":"fake","object":"model"}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		delay := time.Duration(0)
		if bytes.Contains(body, []byte("slowly")) {
			delay = 150 * time.Millisecond
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, word := range strings.SplitAfter(answer, " ") {
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": word}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
			time.Sleep(delay)
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	return listener.Addr().String()
}

func call(client *http.Client, base, token, method, path string, body, result any) {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(req)
	must(err)
	defer response.Body.Close()
	data, _ := io.ReadAll(bufio.NewReader(response.Body))
	if response.StatusCode >= 300 {
		log.Fatalf("%s %s: %s %s", method, path, response.Status, data)
	}
	if result != nil {
		must(json.Unmarshal(data, result))
	}
}

func waitFor(ok func() bool) {
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if ok() {
			return
		}
	}
	log.Fatal("timed out")
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
