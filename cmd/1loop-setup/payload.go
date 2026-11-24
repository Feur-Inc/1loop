// SPDX-License-Identifier: MPL-2.0

// Command 1loop-setup is the Windows installer and uninstaller. It draws its
// own window, in 1loop's colours, without cgo.
//
// The app is at the end of the setup's own file (`1loop-release setup`):
// its zstd, then payloadTail. Without it the same program is the uninstaller,
// which the installer writes beside the app.
//
//	setup.exe [/S] [/D=C:\folder]   uninstall.exe [/S]
//
// Silent (/S), the exit code is 0 when done, 2 when 1loop is running (it is
// never closed for the user), 1 for anything else.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// errRunning: 1loop.exe is open and cannot be replaced or removed.
var errRunning = errors.New("1loop is running: quit it, then try again")

// version is set by the build.
var version = "dev"

const payloadMagic = "1loop setup 1\n"

// payloadTail ends the file: the sizes of the zstd and of the app, the magic.
type payloadTail struct {
	Packed, App uint64
	Magic       [len(payloadMagic)]byte
}

// payload is the app carried by a file, and where the program itself ends.
type payload struct {
	program, packed, app int64
}

// readPayload finds the app at the end of file; nil when it carries none.
func readPayload(file *os.File) (*payload, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	var tail payloadTail
	size := int64(binary.Size(tail))
	if info.Size() < size {
		return nil, nil
	}
	if err := binary.Read(io.NewSectionReader(file, info.Size()-size, size), binary.LittleEndian, &tail); err != nil {
		return nil, err
	}
	if string(tail.Magic[:]) != payloadMagic {
		return nil, nil
	}
	start := info.Size() - size - int64(tail.Packed)
	if start <= 0 || int64(tail.Packed) < 0 || int64(tail.App) <= 0 {
		return nil, errors.New("the setup file is damaged: download it again")
	}
	return &payload{start, int64(tail.Packed), int64(tail.App)}, nil
}

// extract writes the app to a writer, with how much of it is done.
func (p *payload) extract(file *os.File, to io.Writer, progress func(float64)) error {
	reader, err := zstd.NewReader(io.NewSectionReader(file, p.program, p.packed), zstd.WithDecoderMaxWindow(1<<28))
	if err != nil {
		return fmt.Errorf("the setup file is damaged: %w", err)
	}
	defer reader.Close()
	var done int64
	buffer := make([]byte, 1<<18)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			if _, err := to.Write(buffer[:n]); err != nil {
				return err
			}
			done += int64(n)
			progress(float64(done) / float64(p.app))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("the setup file is damaged: %w", err)
		}
	}
	if done != p.app {
		return errors.New("the setup file is damaged: download it again")
	}
	return nil
}
