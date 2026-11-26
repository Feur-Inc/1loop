// SPDX-License-Identifier: MPL-2.0

//go:build !js

package python

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ways known to slip past audit hooks: each must fail.
func TestEscapes(t *testing.T) {
	r := newRig(t)
	secret := filepath.Join(t.TempDir(), "private.txt")
	os.WriteFile(secret, []byte("PRIVATE"), 0600)
	q, _ := json.Marshal(secret)
	for _, code := range []string{
		"import _posixsubprocess, os\nr, w = os.pipe()\n_posixsubprocess.fork_exec(['/bin/cat', %[1]s], [b'/bin/cat'], True, (), None, None, -1, -1, -1, w, -1, -1, r, w, True, False, 0, None, None, None, -1, None, False)\nprint(os.read(r, 99))",
		"import posix; print(posix.system('cat ' + %[1]s))",
		"import importlib.util, os\nspec = importlib.util.find_spec('_posixsubprocess'); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)\nr, w = os.pipe(); m.fork_exec(['/bin/cat', %[1]s], [b'/bin/cat'], True, (), None, None, -1, -1, -1, w, -1, -1, r, w, True, False, 0, None, None, None, -1, None, False); print(os.read(r, 99))",
		"import io; print(io.open_code(%[1]s).read())",
		"import _io; print(_io.FileIO(%[1]s).read())",
		"import sys; sys.audit = lambda *a: None; print(open(%[1]s).read())",
		"import builtins, sys\nfor h in list(sys.__dict__): pass\nprint(open(%[1]s).read())",
		"import _socket; s = _socket.socket(); s.connect(('1.1.1.1', 80)); print('PRIVATE')  # %[1]s",
		"import os; fd = os.open(os.path.dirname(%[1]s), os.O_RDONLY); print(os.listdir(fd), 'PRIVATE')",
		"import shutil; print(shutil.which('sh')); import os; os.execv('/bin/sh', ['sh']) # %[1]s",
		"import importlib.util, sys\nspec = importlib.util.spec_from_file_location('x', %[1]s)\nprint(open(%[1]s).read() if spec else '')",
		"import zipimport, zipfile; print(open(%[1]s, 'rb').read())",
		"import gc; print([o for o in gc.get_objects()][:1], 'PRIVATE')  # %[1]s",
		"import os; os.symlink(%[1]s, 'x'); print(open('x').read())",
		"import ctypes; print(ctypes.CDLL('libc.so.6').system(b'cat ' + %[1]s.encode()))",
		"import ctypes\nprint('PRIVATE' if ctypes.memmove else '')  # %[1]s",
		"import importlib, sys\nc = importlib.import_module('_ctypes')\nb = c.Array  # what ctypes builds on\nimport importlib.util\nspec = importlib.util.find_spec('ctypes')\nm = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)\nbuf = m.create_string_buffer(b'PRIVATE'); print(m.string_at(m.addressof(buf)))  # %[1]s",
		"import importlib, importlib.util\nspec = importlib.util.find_spec('ctypes'); m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)\nm.memmove(0, 0, 0); print('PRIVATE')  # %[1]s",
	} {
		got := r.run(fmt.Sprintf(code, q))
		if got.Error == "" && strings.Contains(got.Printed, "PRIVATE") {
			t.Errorf("escaped:\n%s\n%+v", code, got)
		} else if got.Error == "" {
			t.Logf("ran without error, nothing out:\n%s\n%s", code, got.Printed)
		}
	}
}

// A library may import subprocess; only starting a program is refused.
func TestSubprocessImports(t *testing.T) {
	r := newRig(t)
	got := r.run("import subprocess, platform, uuid\nprint('imported')\nsubprocess.run(['true'])")
	if !strings.Contains(got.Printed, "imported") || !strings.Contains(got.Error, "may not start a program") && !strings.Contains(got.Error, "subprocess.Popen") {
		t.Errorf("%+v", got)
	}
}
