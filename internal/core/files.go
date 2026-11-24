// SPDX-License-Identifier: MPL-2.0

package core

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
)

// WriteFileSafely writes parts, one after the other, to path through a
// temporary file in the same folder that reaches the disk before it
// replaces path: a crash or a power cut leaves the old file or the new one,
// never half of one. With backup, the file it replaces stays as path.bak
// (a second name for it, not a copy).
func WriteFileSafely(path string, perm os.FileMode, backup bool, parts ...[]byte) error {
	return WriteFileStreamed(path, perm, backup, func(w io.Writer) error {
		for _, part := range parts {
			if _, err := w.Write(part); err != nil {
				return err
			}
		}
		return nil
	})
}

// WriteFileStreamed is WriteFileSafely for content written piece by piece,
// through a buffer, by write: a large file (the state, 100 MB with long
// conversations) is never whole in memory (#476).
func WriteFileStreamed(path string, perm os.FileMode, backup bool, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name()) // gone already once renamed
	if err = file.Chmod(perm); err == nil {
		buffered := bufio.NewWriterSize(file, 256<<10)
		if err = write(buffered); err == nil {
			err = buffered.Flush()
		}
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil && backup { // no file to keep: the old backup stays
		os.Remove(path + ".bak")
		os.Link(path, path+".bak") // where links are refused, no backup
	}
	return os.Rename(file.Name(), path)
}
