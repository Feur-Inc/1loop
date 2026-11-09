//go:build wasm

package glfw

// 1loop: text input through a hidden <textarea>. A canvas takes no text, so
// keydown alone typed: no dead keys (´ then e), no input method (Japanese,
// Chinese, Korean), and no phone keyboard, which opens only for a focused
// text field. While the application says a text field has the keyboard
// (SetTextInput) the textarea has the focus; the characters it receives
// (its input and composition events) go to the char callback, and it is
// emptied after each. Keys that do not type (Enter, Backspace, arrows,
// shortcuts) still come from keydown, which then keeps them from the
// textarea.

import (
	"strconv"
	"syscall/js"
	"time"
	"unicode/utf8"
)

// TextRect is a rectangle in window coordinates, those the cursor position
// callback reports.
type TextRect struct{ X, Y, Width, Height float64 }

func (r TextRect) contains(x, y float64) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

type textInput struct {
	area      js.Value // the <textarea>
	active    bool     // a text field has the keyboard (SetTextInput)
	composing bool
	preedit   []rune     // the composition, as typed into the text field so far
	targets   []TextRect // where a tap gives a text field the keyboard
	tapped    time.Time  // when a tap on a target focused the textarea
	caret     TextRect
}

// tapGrace is how long a tap on a target keeps the textarea focused before
// the application says whether a text field took the keyboard.
const tapGrace = 600 * time.Millisecond

var currentWindow *Window

// SetTextInput says whether a text field has the keyboard, and where its
// caret is, for the input method's candidate window.
func SetTextInput(active bool, caret TextRect) {
	w := currentWindow
	if w == nil {
		return
	}
	t := &w.text
	t.active = active
	focused := document.Get("activeElement").Equal(t.area)
	if !active {
		if focused && time.Since(t.tapped) > tapGrace {
			t.area.Call("blur")
		}
		return
	}
	if caret != t.caret {
		t.caret = caret
		style := t.area.Get("style")
		style.Set("left", px(caret.X/w.devicePixelRatio))
		style.Set("top", px(caret.Y/w.devicePixelRatio))
		height := caret.Height / w.devicePixelRatio
		if height < 16 {
			height = 16
		}
		style.Set("height", px(height))
	}
	if !focused {
		t.area.Call("focus", map[string]any{"preventScroll": true})
	}
}

// SetTextInputTargets lists where the text fields are. A phone opens its
// keyboard only for a focus given while handling a tap, before the
// application has seen the tap, so a tap there focuses the textarea at once.
func SetTextInputTargets(targets []TextRect) {
	if w := currentWindow; w != nil {
		w.text.targets = targets
	}
}

func px(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) + "px" }

func (w *Window) startTextInput(body js.Value, newJsFuncFrom func(func(js.Value, []js.Value) any) js.Func) {
	currentWindow = w
	area := document.Call("createElement", "textarea")
	for name, value := range map[string]string{
		"autocomplete": "off", "autocorrect": "off", "autocapitalize": "off", "spellcheck": "false",
		"aria-label": "Text input", "tabindex": "-1",
	} {
		area.Call("setAttribute", name, value)
	}
	// Transparent rather than off-screen, so that the candidate window opens
	// at the caret; 16px, or iOS zooms the page in on focus.
	area.Get("style").Set("cssText", "position:fixed;left:0;top:0;width:2px;height:16px;margin:0;padding:0;border:0;"+
		"opacity:0;color:transparent;background:transparent;caret-color:transparent;resize:none;"+
		"overflow:hidden;white-space:pre;font-size:16px;pointer-events:none;z-index:-1")
	body.Call("appendChild", area)
	w.text.area = area
	t := &w.text

	addListener := func(target js.Value, event string, handler func(js.Value)) {
		target.Call("addEventListener", event, newJsFuncFrom(func(this js.Value, args []js.Value) any {
			handler(args[0])
			return nil
		}))
	}
	addListener(area, "compositionstart", func(js.Value) {
		t.composing = true
		t.preedit = nil
	})
	addListener(area, "compositionupdate", func(e js.Value) {
		w.typePreedit(stringOf(e.Get("data")))
	})
	addListener(area, "compositionend", func(e js.Value) {
		w.typePreedit(stringOf(e.Get("data")))
		t.preedit = nil
		t.composing = false
		area.Set("value", "")
	})
	addListener(area, "input", func(e js.Value) {
		if t.composing || e.Get("isComposing").Bool() {
			return
		}
		text := area.Get("value").String()
		area.Set("value", "")
		w.typeRunes(0, []rune(text))
	})
	addListener(area, "blur", func(js.Value) {
		t.composing = false
		t.preedit = nil
		area.Set("value", "")
	})
	// pointerup comes inside the tap, before the mouse events that tell the
	// application about it.
	addListener(document, "pointerup", func(e js.Value) {
		x := e.Get("clientX").Float() * w.devicePixelRatio
		y := e.Get("clientY").Float() * w.devicePixelRatio
		for _, r := range t.targets {
			if r.contains(x, y) {
				t.tapped = time.Now()
				area.Call("focus", map[string]any{"preventScroll": true})
				return
			}
		}
	})
}

func stringOf(v js.Value) string {
	if v.Type() != js.TypeString {
		return ""
	}
	return v.String()
}

// typePreedit shows the composition in the text field: it erases what of
// the previous one differs, then types the rest.
func (w *Window) typePreedit(text string) {
	t := &w.text
	next := []rune(text)
	same := 0
	for same < len(t.preedit) && same < len(next) && t.preedit[same] == next[same] {
		same++
	}
	w.typeRunes(len(t.preedit)-same, next[same:])
	t.preedit = next
}

// typeRunes erases back characters, then types runes, in order.
func (w *Window) typeRunes(back int, runes []rune) {
	if back == 0 && len(runes) == 0 {
		return
	}
	keyCallback, charCallback := w.keyCallback, w.charCallback
	go func() {
		for ; back > 0 && keyCallback != nil; back-- {
			keyCallback(w, KeyBackspace, -1, Press, 0)
			keyCallback(w, KeyBackspace, -1, Release, 0)
		}
		for _, r := range runes {
			if charCallback != nil {
				charCallback(w, r)
			}
		}
	}()
}

// textKey reports what the textarea, when the key event is its own, does
// with it: compose is a key the input method or a dead key takes, which the
// application must not see; types is a character the textarea's input event
// will send.
func (w *Window) textKey(ke js.Value, mods ModifierKey) (compose, types bool) {
	if !ke.Get("target").Equal(w.text.area) {
		return false, false
	}
	key := ke.Get("key").String()
	if w.text.composing || ke.Get("isComposing").Bool() || ke.Get("keyCode").Int() == 229 || key == "Dead" {
		return true, false
	}
	altGraph := ke.Call("getModifierState", "AltGraph").Bool()
	return false, (mods < 2 || altGraph) && utf8.RuneCountInString(key) == 1
}
