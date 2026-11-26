// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func testWindow() *setupWindow {
	w := &setupWindow{version: "0.3.2", dir: `C:\Users\you\AppData\Local\Programs\1loop`, menu: true, fonts: newFonts(nil, nil),
		quit: func() {}, minimize: func() {}, licence: func() {}, launch: func() {}, browse: func(string) string { return "" }}
	w.post = func(run func()) { run() }
	w.shownAt = time.Now().Add(-time.Hour)
	return w
}

// Every screen draws, at every usual scale, with its controls in the window.
// ONELOOP_SETUP_PREVIEW=DIR keeps the images.
func TestScreens(t *testing.T) {
	for _, scale := range []float64{1, 1.5, 2} {
		w := testWindow()
		w.progress, w.drawn, w.failure = .64, .64, `Cannot write in C:\Program Files\1loop: access is denied`
		for screen, name := range map[view]string{viewWelcome: "welcome", viewWorking: "working", viewDone: "done", viewRunning: "running", viewFailed: "failed", viewRemove: "remove", viewRemoved: "removed"} {
			w.view = screen
			w.hover = "install"
			img := image.NewRGBA(image.Rect(0, 0, int(windowWidth*scale), int(windowHeight*scale)))
			w.draw(img, scale, time.Now())
			for _, c := range w.controls {
				if c.x < 0 || c.y < 0 || c.x+c.w > windowWidth || c.y+c.h > windowHeight || c.press == nil {
					t.Errorf("%s: control %+v", name, c.id)
				}
			}
			if dir := os.Getenv("ONELOOP_SETUP_PREVIEW"); dir != "" {
				var out bytes.Buffer
				png.Encode(&out, img)
				os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%g.png", name, scale)), out.Bytes(), 0o644)
			}
		}
	}
}

// The mouse and the keyboard press what is drawn.
func TestPresses(t *testing.T) {
	w := testWindow()
	installed := make(chan bool, 1)
	w.install = func(dir string, menu, desktop bool, progress func(float64)) error {
		installed <- menu && desktop
		return errRunning
	}
	posted := make(chan func(), 4)
	w.post = func(run func()) { posted <- run }
	draw := func() {
		w.draw(image.NewRGBA(image.Rect(0, 0, windowWidth, windowHeight)), 1, time.Now())
	}
	draw()
	press := func(id string) {
		t.Helper()
		for _, c := range w.controls {
			if c.id == id {
				w.mouseDown(c.x+3, c.y+3)
				w.mouseUp(c.x+3, c.y+3)
				return
			}
		}
		t.Fatalf("no control %q", id)
	}
	press("desktop")
	if !w.desktop {
		t.Fatal("the box was not ticked")
	}
	w.key("tab", false)
	if w.focus != "change" {
		t.Errorf("focus %q", w.focus)
	}
	w.focus = ""
	w.key("enter", false)
	if !<-installed {
		t.Error("installed without the shortcuts asked")
	}
	(<-posted)()
	if w.view != viewRunning {
		t.Errorf("view %d after 1loop was found running", w.view)
	}
}

// What the release writes at the end of the setup comes out of it.
func TestPayload(t *testing.T) {
	app := bytes.Repeat([]byte("the app "), 10000)
	var packed bytes.Buffer
	writer, _ := zstd.NewWriter(&packed)
	writer.Write(app)
	writer.Close()
	tail := payloadTail{Packed: uint64(packed.Len()), App: uint64(len(app))}
	copy(tail.Magic[:], payloadMagic)
	var whole bytes.Buffer
	whole.WriteString("program")
	whole.Write(packed.Bytes())
	binary.Write(&whole, binary.LittleEndian, tail)
	path := filepath.Join(t.TempDir(), "setup.exe")
	os.WriteFile(path, whole.Bytes(), 0o644)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	found, err := readPayload(file)
	if err != nil || found == nil || found.program != int64(len("program")) {
		t.Fatalf("%+v, %v", found, err)
	}
	var out bytes.Buffer
	last := 0.0
	if err := found.extract(file, &out, func(done float64) { last = done }); err != nil || !bytes.Equal(out.Bytes(), app) || last != 1 {
		t.Fatalf("%d bytes, %v, %v", out.Len(), last, err)
	}
	os.WriteFile(path, []byte("program"), 0o644)
	if bare, _ := os.Open(path); bare != nil {
		defer bare.Close()
		if found, err := readPayload(bare); found != nil || err != nil {
			t.Errorf("a program alone: %+v, %v", found, err)
		}
	}
}

func BenchmarkWelcome(b *testing.B) {
	w := testWindow()
	img := image.NewRGBA(image.Rect(0, 0, windowWidth, windowHeight))
	for b.Loop() {
		w.draw(img, 1, time.Now())
	}
}
