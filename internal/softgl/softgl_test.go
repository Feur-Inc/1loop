// SPDX-License-Identifier: MPL-2.0

package softgl

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Windows carries Mesa and extracts it once in the user's local data, with
// its licences; elsewhere there is nothing to extract.
func TestExtract(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LocalAppData", local)
	t.Setenv("XDG_CACHE_HOME", local)
	if runtime.GOOS != "windows" {
		if Available() {
			t.Fatal("only Windows carries Mesa")
		}
		if _, err := Extract(); err == nil {
			t.Fatal("Extract without Mesa succeeded")
		}
		return
	}
	dir, err := Extract()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(local, "1loop", "mesa-"+Version); dir != want {
		t.Fatalf("Extract went to %s, want %s", dir, want)
	}
	for _, name := range []string{"opengl32.dll", "libgallium_wgl.dll", "licenses/Mesa.txt", marker} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Size() == 0 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if !Extracted(dir) {
		t.Fatal("not seen as extracted")
	}
	stamp := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(dir, "opengl32.dll"), stamp, stamp)
	if _, err := Extract(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "opengl32.dll")); !info.ModTime().Equal(stamp) {
		t.Fatal("extracted again")
	}
	os.Remove(filepath.Join(dir, marker))
	if Extracted(dir) {
		t.Fatal("seen as extracted without its marker")
	}
}
