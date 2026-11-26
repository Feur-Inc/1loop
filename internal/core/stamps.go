// SPDX-License-Identifier: MPL-2.0

package core

import (
	"io/fs"
	"os"
	"time"
)

// FileStamps are what os.Stat said of the files and folders a value was
// read from (size, mode, modification time, or that there was none), each
// taken just before it was read: while they all say the same, the value
// still holds, and checking costs a stat each instead of reading and
// parsing them again (#519). A folder's time changes when an entry is
// added, removed or renamed in it.
//
// A file changed less than stampMargin before its stamp could change again
// without its time moving, on a coarse clock or file system: such a stamp
// never holds, and the value is read again until the file is older.
type FileStamps struct {
	entries []fileStamp
}

type fileStamp struct {
	path  string
	found bool
	racy  bool
	size  int64
	mode  fs.FileMode
	mod   time.Time
}

// stampMargin covers the coarsest modification times in use (FAT's 2 s).
const stampMargin = 2 * time.Second

func stampOf(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{path: path}
	}
	return fileStamp{path: path, found: true, size: info.Size(), mode: info.Mode(), mod: info.ModTime()}
}

// Add stamps path, before it is read. A nil FileStamps records nothing.
func (stamps *FileStamps) Add(path string) {
	if stamps == nil {
		return
	}
	stamp := stampOf(path)
	stamp.racy = stamp.found && stamp.mod.After(time.Now().Add(-stampMargin))
	stamps.entries = append(stamps.entries, stamp)
}

// Fresh tells whether every path still says what it said when stamped; a
// nil FileStamps never does.
func (stamps *FileStamps) Fresh() bool {
	if stamps == nil {
		return false
	}
	for _, old := range stamps.entries {
		if old.racy {
			return false
		}
		now := stampOf(old.path)
		if now.found != old.found || now.size != old.size || now.mode != old.mode || !now.mod.Equal(old.mod) {
			return false
		}
	}
	return true
}
