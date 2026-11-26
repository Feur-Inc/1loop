// SPDX-License-Identifier: MPL-2.0

//go:build !js

package ui

import (
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"1loop/internal/palette"
)

func hasColourCloseTo(img image.Image, want color.NRGBA, tol int) bool {
	if img == nil {
		return false
	}
	b := img.Bounds()
	abs := func(a, b int) int {
		if a < b {
			return b - a
		}
		return a - b
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 0xf0 {
				continue
			}
			if abs(int(c.R), int(want.R)) <= tol && abs(int(c.G), int(want.G)) <= tol && abs(int(c.B), int(want.B)) <= tol {
				return true
			}
		}
	}
	return false
}

// Loopy's face ring follows the active accent colour (like 1loop's own ring)
// and its eyes follow colText, updating live on recolour().
func TestLoopyFaceFollowsThemeAccentAndInk(t *testing.T) {
	test.NewTempApp(t)
	defer setColours(palette.Dark, palette.AccentColor(""))

	purple := color.NRGBA{R: 0xa8, G: 0x7b, B: 0xff, A: 0xff}
	setColours(palette.Dark, purple)
	face := newLoopyFace(40, "idle", false, true)
	if !hasColourCloseTo(face.image.Image, colAmber, 12) {
		t.Fatalf("expected Loopy face ring to use purple accent %v", colAmber)
	}
	if !hasColourCloseTo(face.image.Image, colText, 12) {
		t.Fatalf("expected Loopy face eyes to use dark-theme text %v", colText)
	}

	// Switch to light theme with teal accent and recolour in place.
	teal := color.NRGBA{R: 0x2f, G: 0xc2, B: 0xa4, A: 0xff}
	setColours(palette.Light, teal)
	face.recolour()
	if !hasColourCloseTo(face.image.Image, colAmber, 12) {
		t.Fatalf("expected recoloured Loopy face ring to use light-theme teal accent %v", colAmber)
	}
	if !hasColourCloseTo(face.image.Image, colText, 12) {
		t.Fatalf("expected recoloured Loopy face eyes to use light-theme text %v", colText)
	}
}

// Loopy's bubbles use themed surfaces, rounded corners, and respond live to
// theme/accent changes and blur settings.
func TestLoopyBubblesThemeAndLiveSwitch(t *testing.T) {
	app := test.NewTempApp(t)
	defer func() {
		setColours(palette.Dark, palette.AccentColor(""))
		setThemeBlur(palette.ThemeFile{}, false)
		roundness, paintedRoundness = 1, 1
	}()

	purple := color.NRGBA{R: 0xa8, G: 0x7b, B: 0xff, A: 0xff}
	setColours(palette.Dark, purple)
	roundness, paintedRoundness = 0.5, 0.5

	loopyMsg := newLoopyBubble("Hello from Loopy", false, "")
	userMsg := newLoopyBubble("Hello from me", true, "")

	if corner := color.NRGBAModel.Convert(loopyMsg.avatar.image.Image.At(0, 0)).(color.NRGBA); corner.A != 0 {
		t.Fatalf("loopy message avatar corner alpha = %d, want 0 (transparent background)", corner.A)
	}

	if loopyMsg.fill.FillColor != color.Color(colSurface) {
		t.Fatalf("loopy bubble fill = %v, want colSurface %v", loopyMsg.fill.FillColor, colSurface)
	}
	if loopyMsg.fill.StrokeColor != color.Color(colBorderSoft) || loopyMsg.fill.StrokeWidth != 1 {
		t.Fatalf("loopy bubble stroke = %v (w=%v), want colBorderSoft %v (w=1)", loopyMsg.fill.StrokeColor, loopyMsg.fill.StrokeWidth, colBorderSoft)
	}
	if loopyMsg.fill.CornerRadius != 9 {
		t.Fatalf("loopy bubble CornerRadius = %v, want 9 (rounded(18) at 0.5)", loopyMsg.fill.CornerRadius)
	}
	if userMsg.fill.FillColor != color.Color(colAmber) {
		t.Fatalf("user bubble fill = %v, want colAmber %v", userMsg.fill.FillColor, colAmber)
	}
	if userMsg.text.ink != color.Color(colInk) {
		t.Fatalf("user bubble ink = %v, want colInk %v", userMsg.text.ink, colInk)
	}

	// Put both bubbles in a window, switch theme + accent + roundness, and verify live update.
	win := app.NewWindow("loopy-test")
	defer win.Close()
	win.SetContent(loopyMsg.row)
	// Keep userMsg in extraRoots (simulating detached Loopy space while Settings is open).
	savedExtra := extraRoots
	defer func() { extraRoots = savedExtra }()
	extraRoots = append(extraRoots, func() []fyne.CanvasObject { return []fyne.CanvasObject{userMsg.row} })

	teal := color.NRGBA{R: 0x2f, G: 0xc2, B: 0xa4, A: 0xff}
	changed := recolour(setColours(palette.Light, teal))
	repaint(changed, false)
	roundness = 1
	refreshRadii()

	if loopyMsg.fill.FillColor != color.Color(colSurface) {
		t.Fatalf("after theme switch, loopy bubble fill = %v, want %v", loopyMsg.fill.FillColor, colSurface)
	}
	if loopyMsg.fill.CornerRadius != 18 {
		t.Fatalf("after roundness switch, loopy bubble CornerRadius = %v, want 18", loopyMsg.fill.CornerRadius)
	}
	if userMsg.fill.FillColor != color.Color(colAmber) {
		t.Fatalf("after theme switch in detached root, user bubble fill = %v, want %v", userMsg.fill.FillColor, colAmber)
	}
	if userMsg.fill.CornerRadius != 18 {
		t.Fatalf("after roundness switch in detached root, user bubble CornerRadius = %v, want 18", userMsg.fill.CornerRadius)
	}
}

// syncMainFill clears Loopy's mainFill when a shader/picture is behind the
// window (preventing a grey veil) and restores colBackground otherwise.
func TestLoopyMainFillFollowsShaderBehind(t *testing.T) {
	test.NewTempApp(t)
	savedShader := shaderBehind
	defer func() {
		shaderBehind = savedShader
		setColours(palette.Dark, palette.AccentColor(""))
	}()

	setColours(palette.Dark, palette.AccentColor(""))
	ui := &desktopUI{desktopApp: &desktopApp{}}
	ui.mainFill = canvas.NewRectangle(colBackground)
	ui.loopy = &loopyMode{
		mainFill: canvas.NewRectangle(colBackground),
	}

	shaderBehind = true
	ui.syncMainFill()
	if ui.loopy.mainFill.FillColor != color.Color(color.NRGBA{}) {
		t.Fatalf("loopy.mainFill with shaderBehind=true = %v, want transparent", ui.loopy.mainFill.FillColor)
	}

	shaderBehind = false
	ui.syncMainFill()
	if ui.loopy.mainFill.FillColor != color.Color(colBackground) {
		t.Fatalf("loopy.mainFill with shaderBehind=false = %v, want colBackground %v", ui.loopy.mainFill.FillColor, colBackground)
	}
}

// Loopy's response glass syncs its blur and tint with themeBlur/themeBlurTint
// and resets back to colSurface when blur is turned off.
func TestLoopyResponseGlassSyncsBlurAndTint(t *testing.T) {
	test.NewTempApp(t)
	defer func() {
		setColours(palette.Dark, palette.AccentColor(""))
		setThemeBlur(palette.ThemeFile{}, false)
	}()

	setColours(palette.Dark, palette.AccentColor(""))
	bubble := newLoopyBubble("Translucent bubble", false, "")
	if bubble.glass == nil {
		t.Fatal("expected Loopy bubble to have a loopyResponseGlass")
	}

	// Simulate blur tint & reset cycle (works even in headless test where canvas.BlurSupported() may be false).
	tint := color.NRGBA{R: 0x10, G: 0x12, B: 0x20, A: 0x99}
	setThemeBlur(palette.ThemeFile{
		Blur:     map[string]float64{"responses": 16},
		BlurTint: map[string]color.NRGBA{"responses": tint},
	}, true)
	saved := shaderBehind
	defer func() { shaderBehind = saved }()
	// Over the plain window colour there is nothing to blur: no blur, no tint.
	shaderBehind = false
	bubble.syncBlur()
	if bubble.glass.blur.Visible() || bubble.fill.FillColor != color.Color(colSurface) {
		t.Fatalf("without a shader or picture behind: blur visible=%v, fill %v, want hidden and colSurface", bubble.glass.blur.Visible(), bubble.fill.FillColor)
	}
	shaderBehind = true
	bubble.syncBlur()
	if canvas.BlurSupported() {
		if !bubble.glass.blur.Visible() || bubble.glass.blur.Radius != 16 {
			t.Fatalf("expected blur visible with radius 16, got visible=%v radius=%v", bubble.glass.blur.Visible(), bubble.glass.blur.Radius)
		}
		if bubble.fill.FillColor != color.Color(tint) {
			t.Fatalf("bubble fill with blur = %v, want tint %v", bubble.fill.FillColor, tint)
		}
	}

	setThemeBlur(palette.ThemeFile{}, false)
	bubble.syncBlur()
	if bubble.glass.blur.Visible() {
		t.Fatal("expected blur hidden when disabled")
	}
	if bubble.fill.FillColor != color.Color(colSurface) {
		t.Fatalf("bubble fill after disabling blur = %v, want colSurface %v", bubble.fill.FillColor, colSurface)
	}
}

// A bubble waiting for Loopy keeps its dimmed look in the new accent after
// a theme switch: the repaint does not map a half-transparent colour.
func TestLoopyDimmedBubbleFollowsAccent(t *testing.T) {
	test.NewTempApp(t)
	defer setColours(palette.Dark, palette.AccentColor(""))
	setColours(palette.Dark, palette.AccentColor(""))
	bubble := newLoopyBubble("Waiting", true, "")
	bubble.dim()
	teal := color.NRGBA{R: 0x2f, G: 0xc2, B: 0xa4, A: 0xff}
	repaint(recolour(setColours(palette.Dark, teal)), false)
	bubble.recolour()
	if want := color.Color(withAlpha(colAmber, 0.5)); bubble.fill.FillColor != want {
		t.Fatalf("dimmed bubble after an accent switch = %v, want %v", bubble.fill.FillColor, want)
	}
}
