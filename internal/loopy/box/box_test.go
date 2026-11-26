// SPDX-License-Identifier: MPL-2.0

//go:build !js

package box

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var shared = New("")

func run(t *testing.T, folder, script string, limit time.Duration) Result {
	t.Helper()
	result, err := shared.Run(context.Background(), folder, script, limit)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScriptsWorkOnTheFolder(t *testing.T) {
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "dépenses.csv"), []byte("date;quoi;montant\n2026-01-03;courses;\"1 234,50\"\n2026-01-09;train;65,00\n2026-02-01;loyer;700\n"), 0600)
	result := run(t, folder, `
const by = {};
for (const r of csv.parse(files.read("/dépenses.csv"))) { const m = r.date.slice(0, 7); by[m] = (by[m] || 0) + number(r.montant); }
const rows = Object.entries(by).map(([month, total]) => ({month, total}));
files.write("/total.json", JSON.stringify(rows));
console.log(table(rows));`, 0)
	if result.Failed || !strings.Contains(result.Output, "| 2026-01 | 1299.5 |") || !strings.Contains(result.Output, "| 2026-02 | 700 |") {
		t.Fatalf("%+v", result)
	}
	if data, _ := os.ReadFile(filepath.Join(folder, "total.json")); !strings.Contains(string(data), "1299.5") {
		t.Errorf("wrote %s", data)
	}
}

func TestNothingOutOfTheFolder(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	os.WriteFile(secret, []byte("TOPSECRET"), 0600)
	folder := filepath.Join(outside, "home")
	os.Mkdir(folder, 0700)
	os.WriteFile(filepath.Join(folder, "mine.txt"), []byte("mine"), 0600)
	if runtime.GOOS != "windows" {
		os.Symlink(secret, filepath.Join(folder, "link.txt")) // put there by someone else
		os.Symlink(outside, filepath.Join(folder, "up"))
	}
	for _, script := range []string{
		`console.log(std.loadFile("/../secret.txt"))`,
		`console.log(std.loadFile("../secret.txt"))`,
		`console.log(std.loadFile("/home/../../secret.txt"))`,
		`console.log(std.loadFile("` + filepath.ToSlash(secret) + `"))`,
		`console.log(std.loadFile("/link.txt"))`,
		`console.log(std.loadFile("/up/secret.txt"))`,
		`os.symlink("` + filepath.ToSlash(secret) + `", "/new"); console.log(std.loadFile("/new"))`,
		`os.symlink("../secret.txt", "/new2"); console.log(std.loadFile("/new2"))`,
		`os.rename("/up/secret.txt", "/stolen"); console.log(std.loadFile("/stolen"))`,
		`const [n] = os.readdir("/up"); console.log(n); console.log(os.realpath ? os.realpath("/link.txt") : "")`,
		`files.write("/up/written.txt", "x")`,
	} {
		if result := run(t, folder, script, 0); strings.Contains(result.Output, "TOPSECRET") || strings.Contains(result.Output, "secret.txt,") {
			t.Errorf("%s\n-> %s", script, result.Output)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "written.txt")); err == nil {
		t.Error("wrote outside")
	}
	if data, _ := os.ReadFile(secret); string(data) != "TOPSECRET" {
		t.Errorf("the file outside is now %q", data)
	}
}

func TestNoProcessNetworkOrEnvironment(t *testing.T) {
	t.Setenv("SECRET_IN_ENV", "TOPSECRET")
	result := run(t, t.TempDir(), `console.log(typeof os.exec, typeof std.popen, typeof std.urlGet, typeof fetch, typeof os.Worker, JSON.stringify(std.getenviron()))`, 0)
	if strings.Contains(result.Output, "TOPSECRET") || !strings.HasPrefix(result.Output, "undefined undefined undefined undefined") {
		t.Errorf("%+v", result)
	}
}

func TestLimits(t *testing.T) {
	folder := t.TempDir()
	if result := run(t, folder, `while (true) {}`, 500*time.Millisecond); !result.Failed || !strings.Contains(result.Output, "stopped") || result.Took > 5*time.Second {
		t.Errorf("a loop: %+v", result)
	}
	if result := run(t, folder, `const a = []; while (true) a.push(new Array(100000).fill(1.5))`, 20*time.Second); !result.Failed || !strings.Contains(result.Output, "memory") {
		t.Errorf("memory: %+v", result)
	}
	if result := run(t, folder, `for (let i = 0; i < 100000; i++) console.log("a line of output " + i)`, 20*time.Second); len(result.Output) > Output+100 || !strings.Contains(result.Output, "not shown") {
		t.Errorf("output: %d bytes", len(result.Output))
	}
	if result := run(t, folder, `throw new Error("broken")`, 0); !result.Failed || !strings.Contains(result.Output, "broken") {
		t.Errorf("an error: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	if result, _ := shared.Run(ctx, folder, `while (true) {}`, time.Minute); !result.Failed || result.Took > 5*time.Second {
		t.Errorf("Stop: %+v", result)
	}
}

func TestRunsAreApart(t *testing.T) {
	folder := t.TempDir()
	run(t, folder, `globalThis.kept = 1`, 0)
	if result := run(t, folder, `console.log(typeof globalThis.kept)`, 0); strings.TrimSpace(result.Output) != "undefined" {
		t.Errorf("%+v", result)
	}
	done := make(chan string, 4)
	for range 4 {
		go func() {
			result, _ := shared.Run(context.Background(), folder, `let s = 0; for (let i = 0; i < 1e5; i++) s += i; console.log(s)`, 0)
			done <- strings.TrimSpace(result.Output)
		}()
	}
	for range 4 {
		if out := <-done; out != "4999950000" {
			t.Errorf("side by side: %q", out)
		}
	}
}
