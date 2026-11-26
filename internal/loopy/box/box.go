// SPDX-License-Identifier: MPL-2.0

//go:build !js

// Package box is Loopy's small computer: JavaScript in WebAssembly
// (QuickJS-ng on wazero, both inside 1loop, nothing to install). A script
// sees Loopy's folder as / and nothing else: no network, no other file, no
// process, no environment, a bounded memory and time. qjs.wasm is
// quickjs-ng v0.18.0's qjs-wasi.wasm, as released (LICENSE.quickjs).
package box

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	"github.com/tetratelabs/wazero/experimental/sysfs"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed qjs.wasm
var qjs []byte

// Limits of a run.
const (
	Memory  = 256 << 20
	Time    = 30 * time.Second
	MaxTime = 5 * time.Minute
	Output  = 64 << 10 // kept of what it prints
)

// Box runs scripts; it is made at the first one.
type Box struct {
	cache string // where the compiled engine is kept, "" for in memory
	once  sync.Once
	err   error
	run   wazero.Runtime
	code  wazero.CompiledModule
}

// New is a box that keeps its compiled engine in cache.
func New(cache string) *Box { return &Box{cache: cache} }

func (box *Box) open() error {
	box.once.Do(func() {
		ctx := context.Background()
		try := func(config wazero.RuntimeConfig) error {
			config = config.WithMemoryLimitPages(Memory / 65536).WithCloseOnContextDone(true)
			if box.cache != "" {
				if cache, err := wazero.NewCompilationCacheWithDir(box.cache); err == nil {
					config = config.WithCompilationCache(cache)
				}
			}
			run := wazero.NewRuntimeWithConfig(ctx, config)
			if _, err := wasi_snapshot_preview1.Instantiate(ctx, run); err != nil {
				run.Close(ctx)
				return err
			}
			code, err := run.CompileModule(ctx, qjs)
			if err != nil {
				run.Close(ctx)
				return err
			}
			box.run, box.code = run, code
			return nil
		}
		// Compiled for this processor; interpreted, ten times slower, where
		// the system forbids code made at run time.
		if box.err = try(wazero.NewRuntimeConfig()); box.err != nil {
			box.err = try(wazero.NewRuntimeConfigInterpreter())
		}
	})
	return box.err
}

// Result is what a script did.
type Result struct {
	Output string // what it printed, errors included
	Failed bool
	Took   time.Duration
}

// prelude is what every script finds beside the language: files of the
// folder, a CSV reader and the like, so that a script stays short.
const prelude = `
const files = {
  read: p => { const t = std.loadFile(p); if (t === null) throw new Error("no file " + p); return t; },
  write: (p, t) => { const f = std.open(p, "w"); if (!f) throw new Error("cannot write " + p); f.puts(String(t)); f.close(); },
  append: (p, t) => { const f = std.open(p, "a"); if (!f) throw new Error("cannot write " + p); f.puts(String(t)); f.close(); },
  list: (p = "/") => { const [n, e] = os.readdir(p); if (e) throw new Error("no folder " + p); return n.filter(x => x !== "." && x !== "..").sort(); },
  exists: p => os.stat(p)[1] === 0,
  remove: p => { if (os.remove(p) !== 0) throw new Error("cannot remove " + p); },
  mkdir: p => os.mkdir(p),
  json: p => JSON.parse(files.read(p)),
};
const csv = {
  parse: (text, { separator, header = true } = {}) => {
    separator ??= (text.split("\n")[0].match(/;/g) || []).length > (text.split("\n")[0].match(/,/g) || []).length ? ";" : ",";
    const rows = []; let row = [], cell = "", quoted = false;
    for (let i = 0; i < text.length; i++) { const c = text[i];
      if (quoted) { if (c === '"') { if (text[i + 1] === '"') { cell += '"'; i++; } else quoted = false; } else cell += c; }
      else if (c === '"') quoted = true;
      else if (c === separator) { row.push(cell); cell = ""; }
      else if (c === "\n" || c === "\r") { if (c === "\r" && text[i + 1] === "\n") i++; row.push(cell); cell = ""; if (row.length > 1 || row[0] !== "") rows.push(row); row = []; }
      else cell += c; }
    if (cell !== "" || row.length) { row.push(cell); rows.push(row); }
    if (!header) return rows;
    const names = (rows.shift() || []).map(n => n.trim());
    return rows.map(r => Object.fromEntries(names.map((n, i) => [n, r[i] ?? ""])));
  },
  format: rows => { const names = Object.keys(rows[0] || {}); const q = v => /[",\n]/.test(String(v ?? "")) ? '"' + String(v).replace(/"/g, '""') + '"' : String(v ?? ""); return [names, ...rows.map(r => names.map(n => r[n]))].map(r => r.map(q).join(",")).join("\n") + "\n"; },
};
const number = v => { let t = String(v).replace(/[\s  €$£]/g, ""); if (/,\d{1,2}$/.test(t)) t = t.replace(/\./g, "").replace(",", "."); else t = t.replace(/,/g, ""); return parseFloat(t); };
const table = rows => { const names = Object.keys(rows[0] || {}); return ["| " + names.join(" | ") + " |", "|" + names.map(() => "---").join("|") + "|", ...rows.map(r => "| " + names.map(n => r[n]).join(" | ") + " |")].join("\n"); };
`

// Run runs a script over folder, at most limit long (Time when 0).
func (box *Box) Run(ctx context.Context, folder, script string, limit time.Duration) (Result, error) {
	if limit <= 0 {
		limit = Time
	}
	limit = min(limit, MaxTime)
	if err := box.open(); err != nil {
		return Result{}, fmt.Errorf("the box could not start: %w", err)
	}
	root, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return Result{}, err
	}
	// The script's own folder, read only, out of Loopy's.
	code, err := os.MkdirTemp("", "1loop-box-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(code)
	if err := os.WriteFile(filepath.Join(code, "prelude.js"), []byte(prelude), 0600); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(code, "main.js"), []byte(script), 0600); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out := &bounded{limit: Output}
	mounts := wazero.NewFSConfig().WithReadOnlyDirMount(code, "/.1loop").(sysfs.FSConfig).WithSysFSMount(&folderFS{FS: sysfs.DirFS(root), root: root}, "/")
	config := wazero.NewModuleConfig().WithName("").WithArgs("qjs", "--std", "-I", "/.1loop/prelude.js", "/.1loop/main.js").
		WithStdout(out).WithStderr(out).WithFSConfig(mounts).WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	begun := time.Now()
	module, err := box.run.InstantiateModule(ctx, box.code, config)
	if module != nil {
		module.Close(context.Background())
	}
	result := Result{Output: out.String(), Took: time.Since(begun)}
	var exit *sys.ExitError
	switch {
	case err == nil:
	case ctx.Err() != nil:
		result.Failed = true
		result.Output += fmt.Sprintf("\n(stopped after %s)", time.Since(begun).Round(time.Second/10))
	case errors.As(err, &exit):
		result.Failed = exit.ExitCode() != 0
	default:
		result.Failed = true
		result.Output += "\n" + strings.SplitN(err.Error(), "\n", 2)[0]
	}
	return result, nil
}

// bounded keeps the start of what is written.
type bounded struct {
	b     bytes.Buffer
	limit int
	cut   bool
}

func (w *bounded) Write(p []byte) (int, error) {
	if room := w.limit - w.b.Len(); room > 0 {
		w.b.Write(p[:min(room, len(p))])
	}
	w.cut = w.cut || w.b.Len() >= w.limit
	return len(p), nil
}

func (w *bounded) String() string {
	if w.cut {
		return w.b.String() + "\n(the rest is not shown)"
	}
	return w.b.String()
}

// folderFS is a folder whose links lead nowhere: a script can neither make
// one nor follow one out of it.
type folderFS struct {
	experimentalsys.FS
	root string
}

func (fs *folderFS) linked(path string) bool {
	at := fs.root
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return true
		}
		at = filepath.Join(at, part)
		info, err := os.Lstat(at)
		if err != nil {
			return false // not there: the real file system says so
		}
		if info.Mode()&(os.ModeSymlink|os.ModeIrregular|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
			return true
		}
	}
	return false
}

func (fs *folderFS) OpenFile(path string, flag experimentalsys.Oflag, perm os.FileMode) (experimentalsys.File, experimentalsys.Errno) {
	if fs.linked(path) {
		return nil, experimentalsys.EPERM
	}
	return fs.FS.OpenFile(path, flag, perm)
}

func (fs *folderFS) Stat(path string) (sys.Stat_t, experimentalsys.Errno) {
	if fs.linked(path) {
		return sys.Stat_t{}, experimentalsys.EPERM
	}
	return fs.FS.Stat(path)
}

func (fs *folderFS) Mkdir(path string, perm os.FileMode) experimentalsys.Errno {
	if fs.linked(path) {
		return experimentalsys.EPERM
	}
	return fs.FS.Mkdir(path, perm)
}

func (fs *folderFS) Rename(from, to string) experimentalsys.Errno {
	if fs.linked(from) || fs.linked(to) {
		return experimentalsys.EPERM
	}
	return fs.FS.Rename(from, to)
}

func (fs *folderFS) Rmdir(path string) experimentalsys.Errno {
	if fs.linked(path) {
		return experimentalsys.EPERM
	}
	return fs.FS.Rmdir(path)
}

func (fs *folderFS) Unlink(path string) experimentalsys.Errno {
	if fs.linked(filepath.Dir(path)) {
		return experimentalsys.EPERM
	}
	return fs.FS.Unlink(path)
}

func (fs *folderFS) Utimens(path string, atim, mtim int64) experimentalsys.Errno {
	if fs.linked(path) {
		return experimentalsys.EPERM
	}
	return fs.FS.Utimens(path, atim, mtim)
}

func (fs *folderFS) Chmod(string, os.FileMode) experimentalsys.Errno { return experimentalsys.EPERM }
func (fs *folderFS) Symlink(string, string) experimentalsys.Errno    { return experimentalsys.EPERM }
func (fs *folderFS) Link(string, string) experimentalsys.Errno       { return experimentalsys.EPERM }
