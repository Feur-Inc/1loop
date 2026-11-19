// SPDX-License-Identifier: MPL-2.0

package ui

// Shader backgrounds (issue #464): a theme file can name a GLSL fragment
// shader, drawn behind the whole window (third_party/fyne's canvas.Shader)
// with the background and the sidebar laid translucent over it. It is told
// the time, the theme's colours and accent, and what the agent is doing;
// it pauses while the window is hidden or in the background, follows the
// frame rate chosen in Settings, and a shader that does not compile leaves
// the theme's plain colours and its error in Appearance.

import (
	"image/color"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"

	"1loop/internal/palette"
	"1loop/internal/platform"
)

// shaderPrelude is declared before every theme shader: docs/THEMES.md.
const shaderPrelude = `uniform vec2 u_resolution;   // the window, in pixels
uniform vec2 u_origin;       // its bottom left corner in gl_FragCoord
uniform float u_time;        // seconds, paused with the window
uniform vec4 u_background;
uniform vec4 u_surface;
uniform vec4 u_text;
uniform vec4 u_accent;
uniform float u_state;       // STATE_IDLE, STATE_WORKING, STATE_WAITING or STATE_ERROR
uniform float u_state_time;  // seconds since u_state changed
uniform vec4 iMouse;         // xy: the pointer, zw: the last press, in gl_FragCoord's pixels; z < 0 once released
uniform float u_mouse_inside; // 1 while the pointer is over the window, 0 once it left
#define STATE_IDLE 0.0
#define STATE_WORKING 1.0
#define STATE_WAITING 2.0
#define STATE_ERROR 3.0
#line 1
`

var (
	// shaderFPS is the Shader background setting: off, 15, 30 or 60.
	shaderFPS = "30"
	// shaderBroken is the shader file that failed, with its error, until
	// the folder changes.
	shaderBroken, shaderError string
	// shaderBehind is a shader drawn behind the window now: the footer
	// lays no floor over it (glass.go).
	shaderBehind bool
)

// shaderInUse is the shader the active theme draws, "" for none.
func shaderInUse(setting string) (palette.ThemeFile, bool) {
	file, ok := themeFile(setting)
	if !ok || (file.Shader == "" && file.Image == "") || shaderFPS == "off" || (file.Shader != "" && file.Shader == shaderBroken) || (file.Image != "" && file.Image == shaderBroken) {
		return file, false
	}
	return file, true
}

// backdrop is the shader behind the main window.
type backdrop struct {
	shader  *canvas.Shader
	picture *canvas.Image // or a theme's background_image (theme_picture.go)
	picPath string
	picStop chan struct{}
	path    string // the file drawn

	mu           sync.Mutex
	clock        time.Duration // u_time, which stops while paused
	state        float32
	stateChanged time.Duration
	stop         chan struct{}
}

func newBackdrop() *backdrop {
	b := &backdrop{shader: canvas.NewShader(""), picture: canvas.NewImageFromImage(nil)}
	b.shader.Watch = mouseUniforms
	b.shader.Hide()
	b.picture.FillMode = canvas.ImageFillCover
	b.picture.ScaleMode = canvas.ImageScaleSmooth
	b.picture.Hide()
	return b
}

var agentStates = map[trayState]float32{trayIdle: 0, trayWorking: 1, trayWaiting: 2, trayError: 3}

// backdropState tells the shader what the agent does now.
func (ui *desktopUI) backdropState() {
	b := ui.backdrop
	if b == nil {
		return
	}
	state := agentStates[ui.chatsStatus().state]
	b.mu.Lock()
	if state != b.state {
		b.state, b.stateChanged = state, b.clock
	}
	b.mu.Unlock()
}

// syncBackdrop draws the active theme's shader, or none: read again from
// its file each time, so saving the file shows it at once.
func (ui *desktopUI) syncBackdrop() {
	b := ui.backdrop
	if b == nil {
		return
	}
	file, on := shaderInUse(ui.theme.active())
	if shaderBehind != on {
		shaderBehind = on
		if ui.conversation != nil {
			ui.conversation.Refresh()
		}
	}
	ui.syncPicture(file, on)
	if !on || file.Shader == "" {
		if b.stop != nil {
			close(b.stop)
			b.stop = nil
		}
		b.path = ""
		b.shader.Source = ""
		b.shader.Hide()
		return
	}
	data, err := palette.ReadThemeFile(file.Shader)
	if err != nil {
		ui.shaderFailed(file.Shader, err)
		return
	}
	source := shaderPrelude + string(data)
	path := file.Shader
	b.shader.OnError = func(err error) {
		fyne.Do(func() { ui.shaderFailed(path, err) })
	}
	b.path = path
	b.shader.Source = source
	ui.backdropColours()
	b.shader.Show()
	b.shader.Refresh()
	if b.stop == nil {
		b.stop = make(chan struct{})
		go ui.animateBackdrop(b, b.stop)
	}
}

// backdropColours gives the shader the theme's colours.
func (ui *desktopUI) backdropColours() {
	set := func(name string, c color.NRGBA) {
		ui.backdrop.shader.SetUniform(name, float32(c.R)/255, float32(c.G)/255, float32(c.B)/255, 1)
	}
	set("u_background", opaque(colBackground))
	set("u_surface", opaque(colSurface))
	set("u_text", colText)
	set("u_accent", colAmber)
}

func opaque(c color.NRGBA) color.NRGBA { c.A = 0xff; return c }

// shaderFailed falls back to the theme's plain colours and shows why.
func (ui *desktopUI) shaderFailed(path string, err error) {
	platform.Devf("ui", "shader %s: %v", path, err)
	shaderBroken, shaderError = path, err.Error()
	ui.save()
	ui.themeChanged()
}

// animateBackdrop draws frames at the chosen rate while the window is on
// screen and in front; with animations off, one still frame.
func (ui *desktopUI) animateBackdrop(b *backdrop, stop chan struct{}) {
	last := time.Now()
	for {
		fps := 30
		switch shaderFPS {
		case "15":
			fps = 15
		case "60":
			fps = 60
		}
		select {
		case <-stop:
			return
		case <-time.After(time.Second / time.Duration(fps)):
		}
		now := time.Now()
		step := now.Sub(last)
		last = now
		fyne.Do(func() {
			if ui.backdrop != b || b.stop != stop || animationsOff || !ui.inForeground || (ui.tray != nil && ui.tray.hidden) {
				return
			}
			b.mu.Lock()
			b.clock += step
			clock, state, since := b.clock, b.state, b.clock-b.stateChanged
			b.mu.Unlock()
			b.shader.SetUniform("u_time", float32(clock.Seconds()))
			b.shader.SetUniform("u_state", state)
			b.shader.SetUniform("u_state_time", float32(since.Seconds()))
			b.shader.Refresh()
		})
	}
}

// shaderFPSSaved is the setting as saved: empty for the default.
func shaderFPSSaved() string {
	if shaderFPS == "30" {
		return ""
	}
	return shaderFPS
}

// setShaderFPS is the Shader background setting.
func (ui *desktopUI) setShaderFPS(value string) {
	if value == shaderFPS {
		return
	}
	shaderFPS = value
	ui.themeChanged()
}
