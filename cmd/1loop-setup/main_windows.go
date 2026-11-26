// SPDX-License-Identifier: MPL-2.0

package main

import (
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	registerClass   = user32.NewProc("RegisterClassExW")
	createWindow    = user32.NewProc("CreateWindowExW")
	defWindowProc   = user32.NewProc("DefWindowProcW")
	showWindow      = user32.NewProc("ShowWindow")
	setForeground   = user32.NewProc("SetForegroundWindow")
	getMessage      = user32.NewProc("GetMessageW")
	translate       = user32.NewProc("TranslateMessage")
	dispatch        = user32.NewProc("DispatchMessageW")
	postQuit        = user32.NewProc("PostQuitMessage")
	postMessage     = user32.NewProc("PostMessageW")
	destroyWindow   = user32.NewProc("DestroyWindow")
	beginPaint      = user32.NewProc("BeginPaint")
	endPaint        = user32.NewProc("EndPaint")
	invalidate      = user32.NewProc("InvalidateRect")
	setTimer        = user32.NewProc("SetTimer")
	setCapture      = user32.NewProc("SetCapture")
	releaseCapture  = user32.NewProc("ReleaseCapture")
	trackMouse      = user32.NewProc("TrackMouseEvent")
	loadCursor      = user32.NewProc("LoadCursorW")
	setCursor       = user32.NewProc("SetCursor")
	loadIcon        = user32.NewProc("LoadIconW")
	getWindowRect   = user32.NewProc("GetWindowRect")
	setWindowPos    = user32.NewProc("SetWindowPos")
	dpiForSystem    = user32.NewProc("GetDpiForSystem")
	dpiForWindow    = user32.NewProc("GetDpiForWindow")
	dpiAwareness    = user32.NewProc("SetProcessDpiAwarenessContext")
	workArea        = user32.NewProc("SystemParametersInfoW")
	getKeyState     = user32.NewProc("GetKeyState")
	setDIBits       = gdi32.NewProc("SetDIBitsToDevice")
	dwmAttribute    = dwmapi.NewProc("DwmSetWindowAttribute")
	getModuleHandle = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy    = 0x0002
	wmPaint      = 0x000F
	wmClose      = 0x0010
	wmErase      = 0x0014
	wmSetCursor  = 0x0020
	wmNCHitTest  = 0x0084
	wmKeyDown    = 0x0100
	wmTimer      = 0x0113
	wmMouseMove  = 0x0200
	wmLButtonDn  = 0x0201
	wmLButtonUp  = 0x0202
	wmMouseLeave = 0x02A3
	wmDPIChanged = 0x02E0
	wmPosted     = 0x0400 + 1
)

type rect struct{ left, top, right, bottom int32 }

type windowClass struct {
	size, style        uint32
	proc               uintptr
	clsExtra, wndExtra int32
	instance, icon     uintptr
	cursor, background uintptr
	menu, name         *uint16
	iconSmall          uintptr
}

type message struct {
	window  uintptr
	message uint32
	w, l    uintptr
	time    uint32
	x, y    int32
}

type paintInfo struct {
	dc       uintptr
	erase    int32
	rect     rect
	restore  int32
	update   int32
	reserved [32]byte
}

type bitmapInfo struct {
	size                     uint32
	width, height            int32
	planes, bits             uint16
	compression, imageSize   uint32
	xPerMeter, yPerMeter     int32
	colorsUsed, colorsNeeded uint32
}

// screen is the window on Windows: its frames are drawn by setupWindow and
// copied as they are.
type screen struct {
	*setupWindow
	handle   uintptr
	scale    float64
	img      *image.RGBA
	bgra     []byte
	tracking bool
	exit     int

	mu    sync.Mutex
	queue []func()
}

func main() {
	runtime.LockOSThread()
	silent, dir := false, ""
	// /D= is last and holds the rest of the line, as installers have it.
	for i, arg := range os.Args[1:] {
		switch {
		case strings.EqualFold(arg, "/S"):
			silent = true
		case strings.HasPrefix(strings.ToUpper(arg), "/D="):
			dir = strings.Join(append([]string{arg[3:]}, os.Args[i+2:]...), " ")
		}
	}
	removing := false
	if self, err := os.Executable(); err == nil {
		if file, err := os.Open(self); err == nil {
			app, err := readPayload(file)
			removing = app == nil && err == nil
			file.Close()
		}
	}
	if dir == "" {
		dir = defaultDir()
	}
	if silent {
		err := error(nil)
		if removing {
			err = uninstall()
		} else {
			err = install(ownDir(dir), true, false, func(float64) {})
		}
		switch {
		case err == errRunning:
			os.Exit(2)
		case err != nil:
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		if removing {
			leave()
		}
		return
	}
	dpiAwareness.Call(^uintptr(3)) // per monitor, v2
	fonts := filepath.Join(os.Getenv("SystemRoot"), "Fonts")
	regular, _ := os.ReadFile(filepath.Join(fonts, "segoeui.ttf"))
	bold, _ := os.ReadFile(filepath.Join(fonts, "seguisb.ttf"))
	s := &screen{setupWindow: &setupWindow{version: version, dir: ownDir(dir), menu: true, removing: removing, fonts: newFonts(regular, bold)}, scale: 1}
	if dpi, _, _ := dpiForSystem.Call(); dpi != 0 {
		s.scale = float64(dpi) / 96
	}
	s.setupWindow.install, s.remove = install, uninstall
	s.launch = func() { launch(s.dir) }
	s.quit = func() { destroyWindow.Call(s.handle) }
	s.minimize = func() { showWindow.Call(s.handle, 6) }
	s.browse = func(from string) string {
		if picked := pickFolder(windows.HWND(s.handle), from); picked != "" {
			return ownDir(picked)
		}
		return ""
	}
	s.licence = func() {
		page, _ := windows.UTF16PtrFromString("https://www.mozilla.org/MPL/2.0/")
		windows.ShellExecute(windows.Handle(s.handle), nil, page, nil, nil, 1)
	}
	s.post = func(run func()) {
		s.mu.Lock()
		s.queue = append(s.queue, run)
		s.mu.Unlock()
		postMessage.Call(s.handle, wmPosted, 0, 0)
	}
	if removing {
		s.view = viewRemove
	}
	s.shownAt = time.Now()
	s.open()
	var m message
	for {
		if got, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0); int32(got) <= 0 {
			break
		}
		translate.Call(uintptr(unsafe.Pointer(&m)))
		dispatch.Call(uintptr(unsafe.Pointer(&m)))
	}
	if s.view == viewRemoved {
		leave()
	}
}

func (s *screen) open() {
	instance, _, _ := getModuleHandle.Call(0)
	icon, _, _ := loadIcon.Call(instance, 1) // winres
	cursor, _, _ := loadCursor.Call(0, 32512)
	title := "1loop setup"
	if s.removing {
		title = "Uninstall 1loop"
	}
	name, _ := windows.UTF16PtrFromString("1loop-setup")
	class := windowClass{style: 0x00020000 /* a shadow */, proc: syscall.NewCallback(s.event), instance: instance, icon: icon, iconSmall: icon, cursor: cursor, name: name}
	class.size = uint32(unsafe.Sizeof(class))
	registerClass.Call(uintptr(unsafe.Pointer(&class)))
	var area rect
	workArea.Call(0x30, 0, uintptr(unsafe.Pointer(&area)), 0)
	width, height := s.size()
	x, y := area.left+(area.right-area.left-width)/2, area.top+(area.bottom-area.top-height)/2
	handle, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(name)), wide(title), 0x80000000|0x00080000|0x00020000,
		uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, instance, 0)
	s.handle = handle
	round := uint32(2) // Windows 11's corners
	dwmAttribute.Call(handle, 33, uintptr(unsafe.Pointer(&round)), 4)
	if dpi, _, _ := dpiForWindow.Call(handle); dpi != 0 && float64(dpi)/96 != s.scale {
		s.scale = float64(dpi) / 96
		width, height = s.size()
		setWindowPos.Call(handle, 0, 0, 0, uintptr(width), uintptr(height), 0x16)
	}
	showWindow.Call(handle, 5)
	setForeground.Call(handle)
	setTimer.Call(handle, 1, 15, 0)
}

func (s *screen) size() (int32, int32) {
	return int32(windowWidth*s.scale + .5), int32(windowHeight*s.scale + .5)
}

func (s *screen) redraw() { invalidate.Call(s.handle, 0, 0) }

func (s *screen) paint() {
	var info paintInfo
	dc, _, _ := beginPaint.Call(s.handle, uintptr(unsafe.Pointer(&info)))
	defer endPaint.Call(s.handle, uintptr(unsafe.Pointer(&info)))
	width, height := s.size()
	if s.img == nil || s.img.Rect.Dx() != int(width) || s.img.Rect.Dy() != int(height) {
		s.img = image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
		s.bgra = make([]byte, len(s.img.Pix))
	}
	s.draw(s.img, s.scale, time.Now())
	for i := 0; i+3 < len(s.bgra); i += 4 {
		s.bgra[i], s.bgra[i+1], s.bgra[i+2], s.bgra[i+3] = s.img.Pix[i+2], s.img.Pix[i+1], s.img.Pix[i], 0xFF
	}
	header := bitmapInfo{width: width, height: -height, planes: 1, bits: 32}
	header.size = uint32(unsafe.Sizeof(header))
	setDIBits.Call(dc, 0, 0, uintptr(width), uintptr(height), 0, 0, 0, uintptr(height),
		uintptr(unsafe.Pointer(&s.bgra[0])), uintptr(unsafe.Pointer(&header)), 0)
}

func (s *screen) event(window uintptr, kind uint32, w, l uintptr) uintptr {
	// A point of the window, in 96 dpi units.
	at := func() (float64, float64) {
		return float64(int16(l&0xFFFF)) / s.scale, float64(int16(l>>16&0xFFFF)) / s.scale
	}
	switch kind {
	case wmPaint:
		s.paint()
		return 0
	case wmErase:
		return 1
	case wmTimer:
		if s.moving() {
			s.redraw()
		}
		return 0
	case wmPosted:
		s.mu.Lock()
		queue := s.queue
		s.queue = nil
		s.mu.Unlock()
		for _, run := range queue {
			run()
		}
		s.redraw()
		return 0
	case wmNCHitTest: // the top moves the window, buttons apart
		var r rect
		getWindowRect.Call(window, uintptr(unsafe.Pointer(&r)))
		x, y := float64(int32(int16(l&0xFFFF))-r.left)/s.scale, float64(int32(int16(l>>16&0xFFFF))-r.top)/s.scale
		if y < 60 && s.at(x, y) == nil {
			return 2
		}
		return 1
	case wmSetCursor:
		if uint16(l) == 1 {
			shape := uintptr(32512)
			if s.hover != "" {
				shape = 32649 // the hand
			}
			cursor, _, _ := loadCursor.Call(0, shape)
			setCursor.Call(cursor)
			return 1
		}
	case wmMouseMove:
		if !s.tracking {
			track := struct {
				size, flags uint32
				window      uintptr
				hover       uint32
			}{flags: 2, window: window}
			track.size = uint32(unsafe.Sizeof(track))
			trackMouse.Call(uintptr(unsafe.Pointer(&track)))
			s.tracking = true
		}
		was := s.hover
		s.mouseMove(at())
		if s.hover != was {
			s.redraw()
		}
		return 0
	case wmMouseLeave:
		s.tracking, s.hover = false, ""
		s.redraw()
		return 0
	case wmLButtonDn:
		setCapture.Call(window)
		s.mouseDown(at())
		s.redraw()
		return 0
	case wmLButtonUp:
		releaseCapture.Call()
		s.mouseUp(at())
		s.redraw()
		return 0
	case wmKeyDown:
		if name := map[uintptr]string{0x09: "tab", 0x0D: "enter", 0x20: "space", 0x1B: "escape"}[w]; name != "" {
			shift, _, _ := getKeyState.Call(0x10)
			s.key(name, int16(shift) < 0)
			s.redraw()
		}
		return 0
	case wmDPIChanged:
		s.scale = float64(w&0xFFFF) / 96
		r := (*rect)(*(*unsafe.Pointer)(unsafe.Pointer(&l)))
		width, height := s.size()
		setWindowPos.Call(window, 0, uintptr(r.left), uintptr(r.top), uintptr(width), uintptr(height), 0x14)
		s.redraw()
		return 0
	case wmClose:
		if s.view == viewWorking {
			return 0 // half an installation is no use
		}
	case wmDestroy:
		postQuit.Call(0)
		return 0
	}
	result, _, _ := defWindowProc.Call(window, uintptr(kind), w, l)
	return result
}
