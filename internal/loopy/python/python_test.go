// SPDX-License-Identifier: MPL-2.0

//go:build !js

package python

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// system is the machine's Python, so that the tests download nothing; with
// LOOPY_PYTHON, the real one kept there.
func system(t *testing.T) *Python {
	t.Helper()
	if dir := os.Getenv("LOOPY_PYTHON"); dir != "" {
		p := New(dir)
		if err := p.ensure(context.Background(), func(s string) { t.Log(s) }); err != nil {
			t.Fatal(err)
		}
		t.Log(p.Where())
		return p
	}
	for _, name := range []string{"python3", "python"} {
		if exe, err := exec.LookPath(name); err == nil && starts(context.Background(), exe) {
			return &Python{dir: t.TempDir(), exe: exe}
		}
	}
	t.Skip("no Python here")
	return nil
}

type rig struct {
	t    *testing.T
	p    *Python
	job  Job
	call func(name string, args map[string]any) (any, error)
}

func newRig(t *testing.T) *rig {
	dir := t.TempDir()
	r := &rig{t: t, p: system(t)}
	r.job = Job{Dir: dir, Data: filepath.Join(dir, "data"), Out: filepath.Join(dir, "out"), Limit: 60 * time.Second}
	return r
}

func (r *rig) run(code string) Result {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.job.Dir, "main.py"), []byte(code), 0600); err != nil {
		r.t.Fatal(err)
	}
	job := r.job
	job.Call = func(_ context.Context, name string, raw json.RawMessage) (any, error) {
		var args map[string]any
		json.Unmarshal(raw, &args)
		if r.call == nil {
			return nil, errors.New("not here")
		}
		return r.call(name, args)
	}
	result, err := r.p.Run(context.Background(), job)
	if err != nil {
		r.t.Fatal(err)
	}
	return result
}

func TestRunGivesBack(t *testing.T) {
	r := newRig(t)
	r.job.Input = json.RawMessage(`{"n": 3}`)
	r.call = func(name string, args map[string]any) (any, error) {
		if name == "secret" && args["name"] == "école" {
			return map[string]string{"user": "moi", "password": "p@ss-é"}, nil
		}
		return nil, errors.New("no such entry")
	}
	got := r.run(`
import autolink, os, sys
print("début", autolink.input["n"])
s = autolink.secret("école")
print(s, s.user, len(s.password))
try: autolink.secret("x")
except autolink.Error as e: print("refused:", e)
autolink.output({"n": autolink.input["n"] * 2, "é": "ü"})
autolink.output("text")
with open(os.path.join(autolink.out, "a.txt"), "w") as f: f.write("hi")
autolink.file("a.txt")
autolink.file("/etc/passwd")
os.write(1, b"from the descriptor\n")
autolink.state["runs"] = autolink.state.get("runs", 0) + 1
print("runs", autolink.state["runs"])
`)
	if got.Error != "" || len(got.Outputs) != 2 || string(got.Outputs[0]) == "" || len(got.Files) != 1 {
		t.Fatalf("%+v", got)
	}
	for _, want := range []string{"début 3", "Secret(…) moi 6", "refused: no such entry", "from the descriptor", "runs 1", "is not a file"} {
		if !strings.Contains(got.Printed, want) {
			t.Errorf("no %q in %q", want, got.Printed)
		}
	}
	if got := r.run("import autolink\nprint('runs', autolink.state['runs'])\nprint(autolink.new('k',[1,2]), autolink.new('k',[3,1,2]), autolink.new('j',[1],first=True))"); !strings.Contains(got.Printed, "runs 1\n[] [3] [1]") {
		t.Errorf("state: %+v", got)
	}
}

func TestRunTellsItsMistake(t *testing.T) {
	r := newRig(t)
	got := r.run("def f():\n    return 1/0\nf()\n")
	if !strings.Contains(got.Error, "ZeroDivisionError") || !strings.Contains(got.Error, "main.py\", line 2") || strings.Contains(got.Error, "boot.py") {
		t.Errorf("%q", got.Error)
	}
	if got := r.run("import sys; sys.exit(0)"); got.Error != "" {
		t.Errorf("%+v", got)
	}
	if got := r.run("x = ("); !strings.Contains(got.Error, "SyntaxError") {
		t.Errorf("%+v", got)
	}
	if err := r.p.Check(context.Background(), "x = ("); err == nil || !strings.Contains(err.Error(), "SyntaxError") {
		t.Errorf("%v", err)
	}
	if err := r.p.Check(context.Background(), "x = 'é'"); err != nil {
		t.Error(err)
	}
}

func TestRunStops(t *testing.T) {
	r := newRig(t)
	r.job.Limit = 2 * time.Second
	begun := time.Now()
	if got := r.run("while True: pass"); !strings.Contains(got.Error, "stopped after") || time.Since(begun) > 8*time.Second {
		t.Errorf("%+v", got)
	}
	r.job.Limit = time.Minute
	if got := r.run("print('x' * 3_000_000)\n"); len(got.Printed) > Printed+100 || got.Error != "" {
		t.Errorf("%d %s", len(got.Printed), got.Error)
	}
	if got := r.run("import autolink\nautolink.output('x' * 3_000_000)\n"); len(got.Outputs) != 0 || !strings.Contains(got.Printed, "not kept") {
		t.Errorf("%d", len(got.Outputs))
	}
}

// What the guard keeps a script from.
func TestRunStaysInItsPlace(t *testing.T) {
	r := newRig(t)
	secret := filepath.Join(t.TempDir(), "private.txt")
	os.WriteFile(secret, []byte("PRIVATE"), 0600)
	for _, code := range []string{
		"print(open(%s).read())",
		"import os; print(os.read(os.open(%s, os.O_RDONLY), 100))",
		"import pathlib; print(pathlib.Path(%s).read_text())",
		"import os; os.remove(%s)",
		"import os; os.rename(%s, 'here')",
		"import shutil; shutil.copy(%s, 'here'); print(open('here').read())",
		"import os; os.listdir(os.path.dirname(%s)); print('PRIVATE')",
		"open(%s + '2', 'w').write('x'); print('PRIVATE')",
		"import os; os.symlink(%s, 'l'); print(open('l').read())",
		"import subprocess; print(subprocess.check_output(['echo', 'PRIVATE'])) # %s",
		"import os; os.system('echo PRIVATE') # %s",
		"import os; os.popen('echo PRIVATE') # %s",
		"import ctypes; ctypes.CDLL(None).system(b'echo PRIVATE') # %s",
		"import sys; sys.stdin.read() # %s\nraise ValueError()",
		"import multiprocessing as m\nif __name__ == '__main__':\n  p = m.Process(target=print); p.start(); print('PRIVATE') # %s",
	} {
		quoted, _ := json.Marshal(secret)
		got := r.run(fmt.Sprintf(code, quoted))
		if got.Error == "" || strings.Contains(got.Printed, "PRIVATE") {
			t.Errorf("%s: %+v", code, got)
		}
	}
	if data, _ := os.ReadFile(secret); string(data) != "PRIVATE" {
		t.Error("the file was changed")
	}
	if got := r.run("import os\nprint(sorted(os.environ))"); strings.Contains(got.Printed, "PATH'") && runtime.GOOS != "windows" {
		t.Errorf("%s", got.Printed)
	}
}

// Only its sites, whatever way it takes.
func TestRunReachesItsSites(t *testing.T) {
	r := newRig(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "REACHED") }))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	code := fmt.Sprintf("import urllib.request\nprint(urllib.request.urlopen('http://127.0.0.1:%d/').read())", port)
	if got := r.run(code); got.Error == "" || strings.Contains(got.Printed, "REACHED") || !strings.Contains(got.Error, "may not reach 127.0.0.1") {
		t.Errorf("%+v", got)
	}
	raw := fmt.Sprintf("import socket\ns = socket.socket(); s.connect(('127.0.0.1', %d)); s.send(b'GET / HTTP/1.0\\r\\n\\r\\n'); print(s.recv(999))", port)
	if got := r.run(raw); got.Error == "" || strings.Contains(got.Printed, "REACHED") {
		t.Errorf("%+v", got)
	}
	udp := "import socket\nsocket.socket(socket.AF_INET, socket.SOCK_DGRAM).sendto(b'x', ('127.0.0.1', 9))"
	if got := r.run(udp); got.Error == "" {
		t.Errorf("%+v", got)
	}
	r.job.Sites = []string{"127.0.0.1"}
	if got := r.run(code); got.Error != "" || !strings.Contains(got.Printed, "REACHED") {
		t.Errorf("%+v", got)
	}
	r.job.Sites = []string{"localhost"} // a name's own addresses
	if got := r.run(strings.ReplaceAll(code, "127.0.0.1", "localhost")); got.Error != "" || !strings.Contains(got.Printed, "REACHED") {
		t.Errorf("%+v", got)
	}
	if got := r.run("import urllib.request\nurllib.request.urlopen('http://notlocalhost/')"); !strings.Contains(got.Error, "may not reach notlocalhost") {
		t.Errorf("%+v", got)
	}
	// asyncio and threads, as the libraries use them.
	r.job.Sites = []string{"127.0.0.1"}
	async := fmt.Sprintf("import asyncio\nasync def f():\n  r, w = await asyncio.open_connection('127.0.0.1', %d)\n  w.write(b'GET / HTTP/1.0\\r\\n\\r\\n'); await w.drain(); print(await r.read())\nasyncio.run(f())", port)
	if got := r.run(async); got.Error != "" || !strings.Contains(got.Printed, "REACHED") {
		t.Errorf("%+v", got)
	}
}

func TestRequirements(t *testing.T) {
	for text, want := range map[string]bool{"requests": true, "requests>=2.32": true, "a-b_c.d == 1.0, <3": true, "aiohttp[speedups]~=3.9": true,
		"--index-url=http://x": false, "git+https://x/y": false, "./here": false, "x @ http://y/z": false, "": false, "a; import os": false, "https://x/a.whl": false, "-r x": false} {
		if Requirement.MatchString(text) != want {
			t.Errorf("%q: %v", text, !want)
		}
	}
}

func TestUntarStaysInside(t *testing.T) {
	pack := func(headers ...tar.Header) *bytes.Buffer {
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		w := tar.NewWriter(z)
		for _, h := range headers {
			h.Mode = 0644
			w.WriteHeader(&h)
		}
		w.Close()
		z.Close()
		return &b
	}
	for _, h := range []tar.Header{{Name: "../x", Typeflag: tar.TypeReg}, {Name: "/x", Typeflag: tar.TypeReg}, {Name: "a/l", Typeflag: tar.TypeSymlink, Linkname: "../../x"}, {Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/etc"}} {
		if err := untar(pack(h), filepath.Join(t.TempDir(), "o")); err == nil {
			t.Errorf("%+v", h)
		}
	}
	if err := untar(pack(tar.Header{Name: "bin/a", Typeflag: tar.TypeReg}, tar.Header{Name: "bin/b", Typeflag: tar.TypeSymlink, Linkname: "a"}), filepath.Join(t.TempDir(), "o")); err != nil && runtime.GOOS != "windows" {
		t.Error(err)
	}
}

// The real Python, a real library and a real site: LOOPY_PYTHON=folder.
func TestReal(t *testing.T) {
	if os.Getenv("LOOPY_PYTHON") == "" {
		t.Skip("LOOPY_PYTHON")
	}
	r := newRig(t)
	begun := time.Now()
	if err := r.p.Install(context.Background(), r.job.Dir, []string{"ecoledirecte", "requests"}, func(s string) { t.Log(s) }); err != nil {
		t.Fatal(err)
	}
	t.Log("installed in", time.Since(begun))
	r.job.Sites = []string{"api.ecoledirecte.com", "pypi.org"}
	got := r.run(`
import requests, aiohttp, asyncio, sys
print(sys.version, aiohttp.__version__)
print("requests", requests.get("https://pypi.org/pypi/pip/json", timeout=20).status_code)
async def f():
    async with aiohttp.ClientSession() as s:
        async with s.get("https://pypi.org/simple/pip/") as r: print("aiohttp", r.status)
asyncio.run(f())
import ecoledirecte_api.client as ed; print("ecoledirecte", sorted(n for n in dir(ed) if n[0].isupper())[:8])
try: requests.get("https://example.com", timeout=20)
except Exception as e: print("refused", "may not reach example.com" in str(e))
`)
	t.Logf("%s\n%s %s", got.Printed, got.Error, got.Took)
	for _, want := range []string{"requests 200", "aiohttp 200", "ecoledirecte [", "refused True"} {
		if !strings.Contains(got.Printed, want) {
			t.Errorf("no %q", want)
		}
	}
}
