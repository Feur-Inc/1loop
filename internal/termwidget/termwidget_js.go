// SPDX-License-Identifier: MPL-2.0

// Package termwidget is the terminal widget of the side panel. In the
// browser client it is a plain text view of the server's terminal: output
// without its escape sequences, typed characters sent back. Fyne's
// terminal (termwidget.go) would need a js variant of its PTY file.
package termwidget

import (
	"io"
	"regexp"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Config is what the terminal reports when its size changes.
type Config struct {
	Title         string
	Rows, Columns uint
	PWD           string
}

// Terminal shows a remote terminal's output as text.
type Terminal struct {
	widget.BaseWidget
	grid      *widget.TextGrid
	scroll    *container.Scroll
	mu        sync.Mutex
	in        io.WriteCloser
	listeners []chan Config
	text      string
}

// New makes a terminal widget.
func New() *Terminal {
	t := &Terminal{grid: widget.NewTextGrid()}
	t.scroll = container.NewScroll(t.grid)
	t.ExtendBaseWidget(t)
	return t
}

func (t *Terminal) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(t.scroll) }

func (t *Terminal) SetStartDir(string) {}

// Text is what the terminal shows.
func (t *Terminal) Text() string { return t.grid.Text() }

func (t *Terminal) AddListener(listener chan Config) {
	t.mu.Lock()
	t.listeners = append(t.listeners, listener)
	t.mu.Unlock()
	select {
	case listener <- Config{Columns: 100, Rows: 30}:
	default:
	}
}

func (t *Terminal) RemoveListener(listener chan Config) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, l := range t.listeners {
		if l == listener {
			t.listeners = append(t.listeners[:i], t.listeners[i+1:]...)
			return
		}
	}
}

var escapes = regexp.MustCompile(`\x1b(\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[()][0-9A-Za-z]|[=>78])|\r`)

// RunWithConnection shows what out says until it ends; typed text goes to in.
func (t *Terminal) RunWithConnection(in io.WriteCloser, out io.Reader) error {
	t.mu.Lock()
	t.in = in
	t.mu.Unlock()
	buffer := make([]byte, 32<<10)
	for {
		n, err := out.Read(buffer)
		if n > 0 {
			chunk := escapes.ReplaceAllString(string(buffer[:n]), "")
			fyne.Do(func() {
				t.text += chunk
				if len(t.text) > 256<<10 {
					t.text = t.text[len(t.text)-256<<10:]
				}
				t.grid.SetText(t.text)
				t.scroll.ScrollToBottom()
			})
		}
		if err != nil {
			return err
		}
	}
}

func (t *Terminal) send(text string) {
	t.mu.Lock()
	in := t.in
	t.mu.Unlock()
	if in != nil {
		io.WriteString(in, text)
	}
}

func (t *Terminal) FocusGained()            {}
func (t *Terminal) FocusLost()              {}
func (t *Terminal) TypedRune(r rune)        { t.send(string(r)) }
func (t *Terminal) Tapped(*fyne.PointEvent) { fyne.CurrentApp().Driver().CanvasForObject(t).Focus(t) }
func (t *Terminal) TypedKey(key *fyne.KeyEvent) {
	switch key.Name {
	case fyne.KeyReturn, fyne.KeyEnter:
		t.send("\r")
	case fyne.KeyBackspace:
		t.send("\x7f")
	case fyne.KeyTab:
		t.send("\t")
	case fyne.KeyEscape:
		t.send("\x1b")
	case fyne.KeyUp:
		t.send("\x1b[A")
	case fyne.KeyDown:
		t.send("\x1b[B")
	case fyne.KeyRight:
		t.send("\x1b[C")
	case fyne.KeyLeft:
		t.send("\x1b[D")
	}
}
