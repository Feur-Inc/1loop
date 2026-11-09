// SPDX-License-Identifier: MPL-2.0

package discord

import (
	"errors"
	"io"
	"strconv"

	"golang.org/x/sys/windows"
)

// dial opens the first of Discord's named pipes, discord-ipc-0 to 9.
func dial() (io.ReadWriteCloser, error) {
	for i := 0; i < 10; i++ {
		name, _ := windows.UTF16PtrFromString(`\\.\pipe\discord-ipc-` + strconv.Itoa(i))
		handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
		if err == nil {
			return &pipe{handle: handle}, nil
		}
	}
	return nil, errors.New("Discord is not running")
}

// pipe is a named pipe opened for overlapped I/O: the reader waiting for
// Discord's next message must not hold up a write, as it would on a
// synchronous handle.
type pipe struct {
	handle windows.Handle
}

func (p *pipe) Read(b []byte) (int, error) {
	return p.io(b, func(done *uint32, overlapped *windows.Overlapped) error {
		return windows.ReadFile(p.handle, b, done, overlapped)
	})
}

func (p *pipe) Write(b []byte) (int, error) {
	return p.io(b, func(done *uint32, overlapped *windows.Overlapped) error {
		return windows.WriteFile(p.handle, b, done, overlapped)
	})
}

func (p *pipe) io(b []byte, start func(*uint32, *windows.Overlapped) error) (int, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	overlapped := windows.Overlapped{HEvent: event}
	var done uint32
	if err := start(&done, &overlapped); err != nil && err != windows.ERROR_IO_PENDING {
		return 0, err
	}
	if err := windows.GetOverlappedResult(p.handle, &overlapped, &done, true); err != nil {
		return int(done), err
	}
	if done == 0 && len(b) != 0 {
		return 0, io.EOF
	}
	return int(done), nil
}

func (p *pipe) Close() error {
	_ = windows.CancelIoEx(p.handle, nil)
	return windows.CloseHandle(p.handle)
}
