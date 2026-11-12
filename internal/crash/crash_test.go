// SPDX-License-Identifier: MPL-2.0

package crash

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The text cases are shared with the phone's scrubber (CrashReportTest.kt).
func TestScrubText(t *testing.T) {
	var cases struct {
		Home  string
		Names []string
		Cases []struct{ In, Out string }
	}
	data, err := os.ReadFile("testdata/scrub_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	scrubber := Scrubber{Home: cases.Home, Names: cases.Names}
	for _, c := range cases.Cases {
		if got := scrubber.Text(c.In); got != c.Out {
			t.Errorf("Text(%q)\n got %q\nwant %q", c.In, got, c.Out)
		}
	}
}

// A panic in a build from a source folder: 1loop's and its dependencies'
// frames stay, relative to their module; the runtime's go.
const panicTrace = `panic: open /home/margot/clients/acme/secrets.env: permission denied [recovered]
	panic: open /home/margot/clients/acme/secrets.env: permission denied

goroutine 1 [running]:
runtime/debug.Stack()
	/usr/lib/go/src/runtime/debug/stack.go:26 +0x5e
panic({0x1a2b3c0?, 0xc0004a2f30?})
	/usr/lib/go/src/runtime/panic.go:792 +0x132
1loop/internal/ui.(*desktopUI).renderChats(0xc000182008, {0xc0003c4000, 0x12})
	/home/margot/dev/1loop/internal/ui/sidebar.go:412 +0x1a5
1loop/internal/ui.(*desktopUI).load.func2()
	/home/margot/dev/1loop/internal/ui/desktop.go:2071 +0x2d
fyne.io/fyne/v2/internal/driver/glfw.(*gLDriver).Run(0xc0001b6000)
	/home/margot/dev/1loop/third_party/fyne/internal/driver/glfw/driver.go:164 +0x7a
github.com/yuin/goldmark/parser.(*parser).Parse(0xc000ab0000, {0x2b4e5a0, 0xc0004a2f00}, {0x0, 0x0, 0x0})
	/home/margot/go/pkg/mod/github.com/yuin/goldmark@v1.7.8/parser/parser.go:879 +0x28f
net/http.(*conn).serve(0xc0002ac000, {0x2b5c1e8, 0xc0001d2000})
	/usr/lib/go/src/net/http/server.go:2092 +0x5d0
main.main()
	/home/margot/dev/1loop/cmd/1loop/main.go:13 +0x25
created by 1loop/internal/ui.(*desktopUI).startRemote in goroutine 1
	/home/margot/dev/1loop/internal/ui/remote.go:88 +0x1f4
`

func TestScrubTrace(t *testing.T) {
	got := Scrubber{Home: "/home/margot", Names: []string{"margot"}}.Trace(panicTrace)
	want := `panic: open ~/<path>: permission denied [recovered]
	panic: open ~/<path>: permission denied

goroutine 1 [running]:
1loop/internal/ui.(*desktopUI).renderChats(...)
	internal/ui/sidebar.go:412
1loop/internal/ui.(*desktopUI).load.func2(...)
	internal/ui/desktop.go:2071
fyne.io/fyne/v2/internal/driver/glfw.(*gLDriver).Run(...)
	third_party/fyne/internal/driver/glfw/driver.go:164
github.com/yuin/goldmark/parser.(*parser).Parse(...)
	github.com/yuin/goldmark@v1.7.8/parser/parser.go:879
net/http.(*conn).serve(...)
	go/src/net/http/server.go:2092
main.main(...)
	cmd/1loop/main.go:13
created by 1loop/internal/ui.(*desktopUI).startRemote in goroutine 1
	internal/ui/remote.go:88`
	if got != want {
		t.Errorf("Trace:\n%s\n\nwant:\n%s", got, want)
	}
	for _, private := range []string{"margot", "acme", "secrets", "0xc0"} {
		if strings.Contains(strings.ToLower(got), private) {
			t.Errorf("the trace still says %q", private)
		}
	}
}

// A release build (-trimpath) on Windows, and a native crash with its
// registers.
func TestScrubTraceTrimmedAndNative(t *testing.T) {
	trimmed := "panic: runtime error: invalid memory address or nil pointer dereference\n[signal 0xc0000005 code=0x0 addr=0x18 pc=0x7ff6a1b2c3d4]\n\ngoroutine 34 [running]:\n1loop/internal/agent.(*App).Run(0x0, {0x0, 0x0})\n\t1loop/internal/agent/app.go:211 +0x14\nfyne.io/fyne/v2.(*Container).Add(...)\n\tfyne.io/fyne/v2@v2.6.1/container.go:40\n"
	got := Scrubber{Home: `C:\Users\Margot`}.Trace(trimmed)
	want := "panic: runtime error: invalid memory address or nil pointer dereference\n[signal 0xc0000005 code=0x0 addr=0x18 pc=0x7ff6a1b2c3d4]\n\ngoroutine 34 [running]:\n1loop/internal/agent.(*App).Run(...)\n\tinternal/agent/app.go:211\nfyne.io/fyne/v2.(*Container).Add(...)\n\tfyne.io/fyne/v2@v2.6.1/container.go:40"
	if got != want {
		t.Errorf("Trace:\n%s\n\nwant:\n%s", got, want)
	}
	native := "SIGSEGV: segmentation violation\nPC=0x7f3a2b1c0d0e m=5 sigcode=1 addr=0x0\nsignal arrived during cgo execution\n\ngoroutine 1 gp=0xc000002380 m=5 mp=0xc000100008 [syscall, locked to thread]:\nruntime.cgocall(0x1234, 0xc00012)\n\t/usr/lib/go/src/runtime/cgocall.go:167 +0x4b\ngithub.com/go-gl/glfw/v3.3/glfw._Cfunc_glfwPollEvents()\n\t_cgo_gotypes.go:1625 +0x47\n\nrax    0x0\nrbx    0x7f3a2b1c0000\nrip    0x7f3a2b1c0d0e\n"
	got = Scrubber{}.Trace(native)
	want = "SIGSEGV: segmentation violation\nPC=0x7f3a2b1c0d0e m=5 sigcode=1 addr=0x0\nsignal arrived during cgo execution\n\ngoroutine 1 gp=0xc000002380 m=5 mp=0xc000100008 [syscall, locked to thread]:\ngithub.com/go-gl/glfw/v3.3/glfw._Cfunc_glfwPollEvents(...)\n\t_cgo_gotypes.go:1625"
	if got != want {
		t.Errorf("Trace:\n%s\n\nwant:\n%s", got, want)
	}
}

// The developer log: what was said and done is left out entirely; the
// rest is scrubbed as text.
func TestScrubLog(t *testing.T) {
	lines := []string{
		"14:02:11.512 app    1loop a2df0ae, go1.25.0, pid 41230, /home/margot/.local/bin/1loop",
		"14:02:11.530 ui     new chat screen (workspace /home/margot/dev/acme)",
		"14:02:15.004 run    tool bash {\"command\":\"cat ~/.aws/credentials\"}",
		"14:02:15.120 claude assistant: Sure! Your AWS keys are AKIAIOSFODNN7EXAMPLE",
		"14:02:16.300 ui     stop pressed for \"Rewrite the billing service\"",
		"14:02:17.001 perf   slow: open chat took 120ms",
		"14:02:18.000 perf   the interface thread has not turned for 1.6s; goroutines:",
		"goroutine 1 [select]:",
		"1loop/internal/ui.(*desktopUI).persist(0xc000182008, 0x1)",
		"\t/home/margot/dev/1loop/internal/ui/desktop.go:1990 +0x2a5",
	}
	got := Scrubber{Home: "/home/margot", Names: []string{"margot"}}.Log(lines)
	want := []string{
		"14:02:11.512 app 1loop a2df0ae, go1.25.0, pid 41230, ~/<path>",
		"14:02:11.530 ui new chat screen (workspace ~/<path>)",
		"14:02:15.004 run <omitted>",
		"14:02:15.120 claude <omitted>",
		"14:02:16.300 ui stop pressed for \"…\"",
		"14:02:17.001 perf slow: open chat took 120ms",
		"14:02:18.000 perf the interface thread has not turned for 1.6s; goroutines:",
		"goroutine 1 [select]:\n1loop/internal/ui.(*desktopUI).persist(...)\n\tinternal/ui/desktop.go:1990",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Log:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func testCrash() Crash {
	started := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	return Crash{
		Env:     Env{App: "desktop", Version: "0.9.0", Commit: "a2df0ae", Install: "Flatpak", OS: "Linux 6.9.1 (Fedora Linux 40)", Desktop: "KDE", Session: "wayland", Backend: "Wayland", Go: "go1.25.0", Arch: "amd64"},
		Started: started,
		Ended:   started.Add(2*time.Hour + 13*time.Minute),
		Trace:   panicTrace,
		Log:     []string{"14:02:11.530 ui     new chat screen (workspace /home/margot/dev/acme)"},
	}
}

func TestReport(t *testing.T) {
	report := NewReport(testCrash(), Scrubber{Home: "/home/margot", Names: []string{"margot"}})
	if report.Title != "Crash: open ~/<path>: permission denied [recovered]" {
		t.Errorf("title %q", report.Title)
	}
	body := report.Body(false, -1)
	for _, want := range []string{"desktop 0.9.0 (a2df0ae), Flatpak", "Linux 6.9.1 (Fedora Linux 40), amd64", "KDE, wayland, the Wayland build", "go1.25.0", "**Ran for:** 2 h 13 min", "internal/ui/sidebar.go:412"} {
		if !strings.Contains(body, want) {
			t.Errorf("the report lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Developer log") || !strings.Contains(report.Body(true, -1), "(workspace ~/<path>)") {
		t.Error("the developer log is added only when asked")
	}
	link, cut := report.URL(true)
	if cut || !strings.HasPrefix(link, NewIssue+"?") {
		t.Fatalf("link %q cut %v", link, cut)
	}
	query, _ := url.ParseQuery(strings.SplitN(link, "?", 2)[1])
	if query.Get("labels") != "bug" || query.Get("title") != report.Title || query.Get("body") != report.Body(true, -1) {
		t.Errorf("query %v", query)
	}
	if strings.Contains(strings.ToLower(link), "margot") || strings.Contains(link, "acme") {
		t.Errorf("the link names the user: %s", link)
	}
}

// A long trace is cut to fit the link; Copy report keeps all of it.
func TestReportCut(t *testing.T) {
	crash := testCrash()
	var trace strings.Builder
	trace.WriteString("fatal error: concurrent map writes\n\n")
	for i := 0; i < 80; i++ {
		trace.WriteString("goroutine 12 [running]:\n1loop/internal/agent.(*App).loop.func3(0xc000182008)\n\t/home/margot/dev/1loop/internal/agent/loop.go:512 +0x1a5\n1loop/internal/agent.(*App).Run(0xc000182008)\n\t/home/margot/dev/1loop/internal/agent/app.go:211 +0x14\n\n")
	}
	crash.Trace = trace.String()
	report := NewReport(crash, Scrubber{Home: "/home/margot"})
	link, cut := report.URL(false)
	if !cut || len(link) > MaxURL {
		t.Fatalf("cut %v, %d bytes", cut, len(link))
	}
	query, _ := url.ParseQuery(strings.SplitN(link, "?", 2)[1])
	if !strings.Contains(query.Get("body"), "lines cut.") {
		t.Error("the cut is not said")
	}
	if full := report.Text(false); strings.Contains(full, "were cut") || strings.Count(full, "loop.go:512") != 80 {
		t.Error("Copy report must hold the whole trace")
	}
}

func TestNoTrace(t *testing.T) {
	crash := testCrash()
	crash.Trace = ""
	report := NewReport(crash, Scrubber{})
	if report.Title != "Crash: 1loop closed without a trace" || !strings.Contains(report.Body(false, -1), "No Go trace") {
		t.Errorf("%s\n%s", report.Title, report.Body(false, -1))
	}
}

// A clean run leaves nothing; a run that did not end cleanly is offered
// once; an old one is not offered.
func TestStartAndClean(t *testing.T) {
	dir := t.TempDir()
	if Start(dir, Env{App: "desktop"}, "") != nil {
		t.Fatal("a first run has no crash")
	}
	Clean()
	if Start(dir, Env{App: "desktop"}, "") != nil {
		t.Fatal("a clean quit is no crash")
	}
	// This run ends without Clean: killed.
	log := filepath.Join(dir, "dev.log")
	os.WriteFile(log, []byte("10:00:00.000 app    started\n10:00:01.000 ui     click\n"), 0600)
	mu.Lock()
	current = "" // as if the process died here
	mu.Unlock()
	os.WriteFile(filepath.Join(dir, "running.json"), []byte(`{"env":{"app":"desktop","version":"0.9.0"},"started":"`+time.Now().Add(-time.Hour).Format(time.RFC3339)+`","devLog":"`+log+`"}`), 0600)
	last := Start(dir, Env{App: "desktop"}, "")
	if last == nil || last.Trace != "" || last.Env.Version != "0.9.0" || len(last.Log) != 2 {
		t.Fatalf("killed run: %+v", last)
	}
	Clean()
	if Start(dir, Env{}, "") != nil {
		t.Fatal("a crash is offered once")
	}
	Clean()
	// A panic's trace, from a week ago.
	os.WriteFile(filepath.Join(dir, "crash.log"), []byte(panicTrace), 0600)
	old := time.Now().Add(-7 * 24 * time.Hour)
	os.Chtimes(filepath.Join(dir, "crash.log"), old, old)
	if Start(dir, Env{}, "") != nil {
		t.Fatal("an old crash is dropped")
	}
	Clean()
	os.WriteFile(filepath.Join(dir, "crash.log"), []byte(panicTrace), 0600)
	if last := Start(dir, Env{}, ""); last == nil || !strings.Contains(last.Trace, "permission denied") {
		t.Fatalf("panic: %+v", last)
	}
	Clean()
}

// A signal's dump (GOTRACEBACK=crash): frame addresses after the line, and
// the runtime's own goroutines, which are left out whole.
func TestScrubSignalDump(t *testing.T) {
	dump := `SIGABRT: abort
PC=0x49b9c1 m=0 sigcode=0

goroutine 0 gp=0x2fa5640 m=0 mp=0x2fa7a80 [idle]:
runtime.futex(0x2fa7bd8, 0x80, 0x0, 0x0, 0x0, 0x0)
	/usr/lib/go/src/runtime/sys_linux_amd64.s:575 +0x21 fp=0x7fff28a042b0 sp=0x7fff28a042a8 pc=0x49b9c1

goroutine 3 gp=0x3b1f0f2092c0 m=nil [GC sweep wait]:
runtime.gopark(0x0?, 0x0?, 0x0?, 0x0?, 0x0?)
	/usr/lib/go/src/runtime/proc.go:460 +0xce fp=0x3b1f0f2a7f38 sp=0x3b1f0f2a7f18 pc=0x4949ce
created by runtime.gcenable in goroutine 1
	/usr/lib/go/src/runtime/mgc.go:214 +0x66

goroutine 68 gp=0x3b1f0f3305a0 m=nil [IO wait]:
internal/poll.runtime_pollWait(0x7f0c1c2b1e00, 0x72)
	/usr/lib/go/src/runtime/netpoll.go:351 +0x85 fp=0x3b1f0f0b5b60 sp=0x3b1f0f0b5b40 pc=0x48e4a5
github.com/fsnotify/fsnotify.(*inotify).readEvents(0x3b1f0f304d20)
	/home/margot/go/pkg/mod/github.com/fsnotify/fsnotify@v1.9.0/backend_inotify.go:357 +0x1a5 fp=0x3b1f0f0b5fc8 sp=0x3b1f0f0b5d40 pc=0x8a3e45
created by 1loop/internal/ui.(*desktopUI).watch in goroutine 1
	/home/margot/dev/1loop/internal/ui/watch.go:40 +0x2b

rax    0xca
rbx    0x0
rip    0x49b9c1
`
	got := Scrubber{Home: "/home/margot"}.Trace(dump)
	want := `SIGABRT: abort
PC=0x49b9c1 m=0 sigcode=0

goroutine 68 gp=0x3b1f0f3305a0 m=nil [IO wait]:
internal/poll.runtime_pollWait(...)
	go/src/runtime/netpoll.go:351
github.com/fsnotify/fsnotify.(*inotify).readEvents(...)
	github.com/fsnotify/fsnotify@v1.9.0/backend_inotify.go:357
created by 1loop/internal/ui.(*desktopUI).watch in goroutine 1
	internal/ui/watch.go:40`
	if got != want {
		t.Errorf("Trace:\n%s\n\nwant:\n%s", got, want)
	}
}
