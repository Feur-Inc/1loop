// SPDX-License-Identifier: MPL-2.0

// The browser client's smoke run (issue #267): a fake streaming model, a
// 1loop-server serving the web build at /app/, and headless Chromium
// loading it, with screenshots and load timings in -out.
//
//	go run ./cmd/1loop-web-smoke -server bin/1loop-server -web bin/web -out .out/web
//
// Canvas positions (Fyne draws everything in one <canvas>) are flags, read
// off an earlier run's screenshots. CI runs it with -check and no positions,
// where files still reach the composer through the page's file input, a
// paste and a drop:
//
//	go run ./cmd/1loop-web-smoke -long 5 -check
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
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
	"sync/atomic"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
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
	picker    = flag.String("picker", "", "x,y of the composer's model button, to show its picker")
	copyAt    = flag.String("copy", "", "x,y of a Copy button, to read the clipboard after")
	attachAt  = flag.String("attach", "", "x,y of the composer's + button, to attach a file and paste a picture before -prompt sends")
	filesItem = flag.String("attach-files", "", "x,y of the + menu's Attach files… item")
	imeAt     = flag.String("ime", "", "x,y of the composer, to type through a dead key and an input method there")
	phone     = flag.String("phone", "", "x,y of the composer in a phone's viewport (390×844, touch), to tap and type there")
	longChat  = flag.Int("long", 60, "turns in the long chat")
	check     = flag.Bool("check", false, "fail unless the page pairs, reads the server's state and events, and never panics (CI)")
)

// stopServer ends the server on every exit, log.Fatal's included (fatal):
// a server left running keeps its port and the next run reaches it instead.
var stopServer = func() {}

func main() {
	flag.Parse()
	must(os.MkdirAll(*outDir, 0o755))
	data, err := os.MkdirTemp(*outDir, "server-")
	must(err)
	projects := filepath.Join(data, "projects")
	must(os.MkdirAll(projects, 0o755))
	model := fakeModel()

	// The server, its console on a pipe for pairing.
	logFile, err := os.Create(filepath.Join(*outDir, "server.log"))
	must(err)
	free, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	listen := free.Addr().String()
	free.Close()
	server := exec.Command(*serverBin, "-listen", listen, "-data-dir", filepath.Join(data, "state"), "-workspace", projects,
		"-provider", "compatible", "-base-url", "http://"+model+"/v1", "-model", "fake", "-web-dir", *webDir)
	console, err := server.StdinPipe()
	must(err)
	server.Stdout, server.Stderr = logFile, logFile
	server.Env = append(os.Environ(), "HOME="+data, "XDG_CONFIG_HOME="+filepath.Join(data, "config"), "ONELOOP_UPDATE_URL=http://127.0.0.1:9/none")
	must(server.Start())
	stopServer = func() { server.Process.Kill() }
	defer stopServer()
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
	// The console takes the approval when it gets to it: 409 until then.
	waitFor(func() bool {
		return try(insecure, base, "", "POST", "/api/pair/confirm", map[string]string{"pairing": pair.Pairing, "code": pair.Code}, &paired) != http.StatusConflict
	})
	token := paired.Token
	var project struct{ Workspace string }
	call(insecure, base, token, "POST", "/api/projects", map[string]any{"path": "project", "create": true}, &project)
	workspace := project.Workspace
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
	chooser := make(chan cdp.BackendNodeID, 1)
	var streamMu sync.Mutex
	events, states := 0, 0
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
			if strings.HasSuffix(e.Response.URL, "/api/state") && e.Response.Status == http.StatusOK {
				streamMu.Lock()
				states++
				streamMu.Unlock()
			}
		case *page.EventFileChooserOpened:
			chooser <- e.BackendNodeID
		case *runtime.EventExceptionThrown:
			streamMu.Lock()
			consoleLines = append(consoleLines, "uncaught: "+e.ExceptionDetails.Error())
			streamMu.Unlock()
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
		failIf(*check, "the page never asked to pair")
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
	if *picker != "" {
		click(ctx, *picker)
		time.Sleep(1500 * time.Millisecond)
		shot(ctx, "05-model-picker")
		chromedp.Run(ctx, chromedp.KeyEvent(kb.Escape))
		time.Sleep(500 * time.Millisecond)
	}
	attached := 0
	if *check && (*attachAt == "" || *prompt == "") {
		attached = attachWithoutPositions(ctx, projects)
	}
	if *prompt != "" {
		click(ctx, *prompt)
		time.Sleep(time.Second)
		typeText(ctx, "Please answer slowly: é à ü ç — ünïcödé ✓ @#{[|")
		time.Sleep(time.Second)
		shot(ctx, "06-typed")
		if *attachAt != "" {
			attachThroughMenu(ctx, chooser)
			click(ctx, *prompt)
			pasteImage(ctx)
			time.Sleep(time.Second)
			pasteText(ctx, " (and pasted text)")
			time.Sleep(2 * time.Second)
			shot(ctx, "06-attached")
			attached = waitAttachments(projects, 2)
		}
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
		if *attachAt != "" {
			log.Printf("the model got: a picture %v, the attached files' list %v", modelSawImage.Load(), modelSawFiles.Load())
			failIf(*check && !(modelSawImage.Load() && modelSawFiles.Load()), "the sent message lacks its attachments")
		}
	}
	if *copyAt != "" {
		chromedp.Run(ctx, browserGrant())
		click(ctx, *copyAt)
		time.Sleep(time.Second)
		var text string
		err := chromedp.Run(ctx, chromedp.Evaluate(`navigator.clipboard.readText()`, &text, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		log.Printf("clipboard after Copy: %q (%v)", text, err)
	}
	// What the browser hands the page as text (glfw-js's hidden textarea):
	// a dead key's composition, an input method's, a phone keyboard's.
	var typed []string
	expect := func(ctx context.Context, step, want string) {
		var text string
		chromedp.Run(ctx, chromedp.Evaluate(`window.loopFocusedText ? loopFocusedText() : ""`, &text))
		log.Printf("%s: the focused entry reads %q", step, text)
		if !strings.Contains(text, want) {
			typed = append(typed, fmt.Sprintf("%s: %q lacks %q", step, text, want))
		}
	}
	if *imeAt != "" {
		click(ctx, *imeAt)
		time.Sleep(time.Second)
		typeText(ctx, "Dead key: ")
		// ^ then e on a French keyboard: the dead key starts a composition,
		// the next key ends it with ê.
		chromedp.Run(ctx,
			input.DispatchKeyEvent(input.KeyRawDown).WithKey("Dead").WithCode("BracketLeft").WithWindowsVirtualKeyCode(219),
			input.ImeSetComposition("^", 1, 1),
			input.DispatchKeyEvent(input.KeyUp).WithKey("Dead").WithCode("BracketLeft").WithWindowsVirtualKeyCode(219))
		time.Sleep(500 * time.Millisecond)
		shot(ctx, "08-dead-key-composing")
		chromedp.Run(ctx, input.InsertText("ê"))
		typeText(ctx, "tre, IME: ")
		time.Sleep(500 * time.Millisecond)
		expect(ctx, "dead key", "Dead key: être, IME: ")
		// Japanese: the preedit grows as one types, then becomes kanji.
		for _, preedit := range []string{"に", "にほ", "にほん", "日本"} {
			n := int64(len([]rune(preedit)))
			chromedp.Run(ctx, input.ImeSetComposition(preedit, n, n))
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(500 * time.Millisecond)
		shot(ctx, "09-ime-composing")
		expect(ctx, "IME preedit", "IME: 日本")
		chromedp.Run(ctx, input.InsertText("日本語"))
		typeText(ctx, " ok")
		time.Sleep(time.Second)
		shot(ctx, "10-ime-committed")
		expect(ctx, "IME committed", "IME: 日本語 ok")
	}
	if *phone != "" {
		phoneCtx, cancelPhone := chromedp.NewContext(ctx)
		defer cancelPhone()
		must(chromedp.Run(phoneCtx,
			emulation.SetDeviceMetricsOverride(390, 844, 2, true),
			emulation.SetTouchEmulationEnabled(true).WithMaxTouchPoints(5),
			chromedp.Navigate(base+"/app/")))
		waitFor(func() bool {
			chromedp.Run(phoneCtx, chromedp.Evaluate(`window.loopTimings && window.loopTimings.instantiated || 0`, &loaded))
			return loaded > 0
		})
		time.Sleep(5 * time.Second)
		shot(phoneCtx, "11-phone")
		// A phone opens its keyboard only for a text field focused during
		// the tap: the focus must be there as soon as the tap is handled,
		// not when the page next looks at Fyne's focus. A tap elsewhere
		// first takes it away.
		// window's listener runs right after glfw-js's on document, within
		// the same dispatch of the tap's pointerup.
		chromedp.Run(phoneCtx, chromedp.Evaluate(`addEventListener("pointerup", () => window.loopTapFocus = document.activeElement.tagName)`, nil))
		focusAfterTap := func(x, y float64) string {
			var focus string
			chromedp.Run(phoneCtx,
				input.DispatchTouchEvent(input.TouchStart, []*input.TouchPoint{{X: x, Y: y}}),
				input.DispatchTouchEvent(input.TouchEnd, []*input.TouchPoint{}),
				chromedp.Evaluate(`window.loopTapFocus || ""`, &focus))
			return focus
		}
		focusAfterTap(100, 450)
		time.Sleep(time.Second)
		var focus string
		chromedp.Run(phoneCtx, chromedp.Evaluate(`document.activeElement.tagName`, &focus))
		log.Printf("phone: after a tap outside the composer the focus is on %s", focus)
		if focus == "TEXTAREA" {
			typed = append(typed, "phone: a tap outside the composer left the keyboard open")
		}
		focus = focusAfterTap(xy(*phone))
		log.Printf("phone: during the tap on the composer the focus went to %s", focus)
		if focus != "TEXTAREA" {
			typed = append(typed, "phone: the tap on the composer left the focus on "+focus)
		}
		time.Sleep(time.Second)
		// A soft keyboard composes each word, then commits it.
		for _, word := range []string{"Hello", " from", " a", " phone"} {
			for i := range word {
				n := int64(i + 1)
				chromedp.Run(phoneCtx, input.ImeSetComposition(word[:i+1], n, n))
			}
			chromedp.Run(phoneCtx, input.InsertText(word))
		}
		chromedp.Run(phoneCtx, input.InsertText(" ✓"))
		time.Sleep(time.Second)
		shot(phoneCtx, "12-phone-typed")
		expect(phoneCtx, "phone", "Hello from a phone ✓")
	}
	for _, problem := range typed {
		log.Printf("text input: %s", problem)
	}

	streamMu.Lock()
	os.WriteFile(filepath.Join(*outDir, "console.log"), []byte(strings.Join(consoleLines, "\n")), 0o644)
	log.Printf("first pairing request %s after navigation", firstFrame.Round(time.Millisecond))
	if *check {
		// Go's runtime prints a panic to the console before the page dies.
		for _, line := range consoleLines {
			failIf(strings.Contains(line, "panic:") || strings.Contains(line, "fatal error:") || strings.HasPrefix(line, "uncaught: "),
				"the page crashed: "+line)
		}
		failIf(states == 0, "the page never read /api/state")
		failIf(events == 0, "the page never opened /api/events")
		failIf(len(typed) > 0, "text input: "+strings.Join(typed, "; "))
		failIf(attached < 2, fmt.Sprintf("%d attachment(s) reached the server", attached))
		log.Printf("check passed: %d state reads, %d event streams, %d attachments", states, events, attached)
	}
	streamMu.Unlock()
}

// failIf ends the run with the server's log, for a CI run that has no
// screenshots to look at.
func failIf(failed bool, why string) {
	if !failed {
		return
	}
	if data, err := os.ReadFile(filepath.Join(*outDir, "server.log")); err == nil {
		os.Stderr.Write(data)
	}
	fatal(why)
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

// typeText types as a keyboard does: chromedp sends the characters it has
// no key for as "Unidentified", where a real é key's keydown says "é".
func typeText(ctx context.Context, text string) {
	for _, r := range text {
		if _, known := kb.Keys[r]; known {
			chromedp.Run(ctx, chromedp.KeyEvent(string(r)))
			continue
		}
		key := string(r)
		chromedp.Run(ctx,
			input.DispatchKeyEvent(input.KeyDown).WithKey(key).WithText(key).WithUnmodifiedText(key),
			input.DispatchKeyEvent(input.KeyUp).WithKey(key))
	}
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

// attachThroughMenu picks a file as a person does: the + menu's Attach
// files… opens the browser's chooser (which only a click may do), and the
// harness answers it.
func attachThroughMenu(ctx context.Context, chooser chan cdp.BackendNodeID) {
	file := sampleFile("notes.txt", []byte("Notes attached from the browser.\n"))
	must(chromedp.Run(ctx, page.SetInterceptFileChooserDialog(true)))
	click(ctx, *attachAt)
	time.Sleep(time.Second)
	shot(ctx, "06-attach-menu")
	click(ctx, *filesItem)
	select {
	case node := <-chooser:
		log.Printf("Attach files… opened the file chooser")
		must(chromedp.Run(ctx, dom.SetFileInputFiles([]string{file}).WithBackendNodeID(node)))
	case <-time.After(10 * time.Second):
		log.Printf("Attach files… opened no file chooser")
		failIf(*check, "Attach files… opened no file chooser")
	}
}

// pasteImage copies a picture and presses Ctrl+V in the page, which pastes
// as the browser's own paste would.
func pasteImage(ctx context.Context) {
	chromedp.Run(ctx, browserGrant())
	write := fmt.Sprintf(`(async () => {
		const bytes = Uint8Array.from(atob(%q), c => c.charCodeAt(0));
		await navigator.clipboard.write([new ClipboardItem({"image/png": new Blob([bytes], {type: "image/png"})})]);
		return "ok";
	})()`, base64.StdEncoding.EncodeToString(samplePNG()))
	var result string
	if err := chromedp.Run(ctx, chromedp.Evaluate(write, &result, awaitPromise)); err != nil {
		log.Printf("clipboard write: %v", err)
	}
	pressCtrlV(ctx)
}

// pasteText pastes text, which goes to the composer as before pictures did.
func pasteText(ctx context.Context, text string) {
	var result string
	if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`navigator.clipboard.writeText(%q).then(() => "ok")`, text), &result, awaitPromise)); err != nil {
		log.Printf("clipboard write: %v", err)
	}
	pressCtrlV(ctx)
}

func pressCtrlV(ctx context.Context) {
	ctrlV := input.DispatchKeyEvent(input.KeyDown).WithKey("v").WithCode("KeyV").WithWindowsVirtualKeyCode(86).
		WithModifiers(input.ModifierCtrl).WithCommands([]string{"paste"})
	chromedp.Run(ctx, ctrlV, input.DispatchKeyEvent(input.KeyUp).WithKey("v").WithCode("KeyV").WithWindowsVirtualKeyCode(86).WithModifiers(input.ModifierCtrl))
}

// attachWithoutPositions gives the page a file through its file input, a
// pasted picture and a dropped file, as the browser would, for a run that
// knows no positions on the canvas.
func attachWithoutPositions(ctx context.Context, projects string) int {
	file := sampleFile("notes.txt", []byte("Notes attached from the browser.\n"))
	must(chromedp.Run(ctx, chromedp.SetUploadFiles("#loop-attach", []string{file}, chromedp.ByID)))
	events := fmt.Sprintf(`(() => {
		const bytes = Uint8Array.from(atob(%q), c => c.charCodeAt(0));
		const pasted = new DataTransfer();
		pasted.items.add(new File([bytes], "image.png", {type: "image/png"}));
		document.dispatchEvent(new ClipboardEvent("paste", {clipboardData: pasted, bubbles: true, cancelable: true}));
		const dropped = new DataTransfer();
		dropped.items.add(new File(["dropped on the page\n"], "dropped.txt", {type: "text/plain"}));
		const canvas = document.querySelector("canvas");
		canvas.dispatchEvent(new DragEvent("dragover", {dataTransfer: dropped, bubbles: true, cancelable: true}));
		canvas.dispatchEvent(new DragEvent("drop", {dataTransfer: dropped, bubbles: true, cancelable: true}));
		return "ok";
	})()`, base64.StdEncoding.EncodeToString(samplePNG()))
	var result string
	if err := chromedp.Run(ctx, chromedp.Evaluate(events, &result)); err != nil {
		log.Printf("paste and drop: %v", err)
	}
	attached := waitAttachments(projects, 3)
	shot(ctx, "06-attached-without-positions")
	return attached
}

// waitAttachments waits for want files in the server projects' tmp
// folders, where uploaded attachments go, and says how many there are.
func waitAttachments(projects string, want int) int {
	var names []string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		names = nil
		filepath.WalkDir(projects, func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && filepath.Base(filepath.Dir(path)) == "tmp" {
				names = append(names, entry.Name())
			}
			return nil
		})
		if len(names) >= want {
			break
		}
	}
	log.Printf("attachments on the server: %v", names)
	return len(names)
}

func sampleFile(name string, data []byte) string {
	path, err := filepath.Abs(filepath.Join(*outDir, name))
	must(err)
	must(os.WriteFile(path, data, 0o644))
	return path
}

// samplePNG is a small picture for pasting: a diagonal gradient.
func samplePNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 240, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 240; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y + 60), B: 0xd0, A: 0xff})
		}
	}
	var buf bytes.Buffer
	must(png.Encode(&buf, img))
	return buf.Bytes()
}

func awaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }

// modelSawImage and modelSawFiles are what the fake model was last sent:
// a picture, and the list of attached files.
var modelSawImage, modelSawFiles atomic.Bool

// fakeModel answers like an OpenAI-compatible API, streaming word by word:
// slowly when asked to, for watching a run arrive in the browser.
func fakeModel() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	answer := strings.Repeat("Here is a **markdown** answer with `code`, a list:\n\n- one point\n- another point\n\n", 4) +
		"```go\nfunc main() {\n\tfmt.Println(\"hello from the fake model\")\n}\n```\n"
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"data":[{"id":"fake","object":"model"},{"id":"gpt-5.5","object":"model"},{"id":"gpt-5.4-mini","object":"model"},{"id":"qwen3-coder-30b","object":"model"},{"id":"qwen3-8b","object":"model"}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("data:image/png;base64")) {
			modelSawImage.Store(true)
		}
		if bytes.Contains(body, []byte("[Attached files]")) {
			modelSawFiles.Store(true)
		}
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
	try(client, base, token, method, path, body, result)
}

// try is call, except that it returns a 409 Conflict instead of failing.
func try(client *http.Client, base, token, method, path string, body, result any) int {
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
	if response.StatusCode == http.StatusConflict {
		return response.StatusCode
	}
	if response.StatusCode >= 300 {
		fatal(fmt.Sprintf("%s %s: %s %s", method, path, response.Status, data))
	}
	if result != nil {
		must(json.Unmarshal(data, result))
	}
	return response.StatusCode
}

func waitFor(ok func() bool) {
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if ok() {
			return
		}
	}
	fatal("timed out")
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(v any) {
	stopServer()
	log.Fatal(v)
}
