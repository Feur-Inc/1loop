// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"

	"1loop/internal/discord"
	"1loop/internal/loopy"
	"1loop/internal/platform"
)

// discordAppID is 1loop's application on Discord: its name is what the
// profile shows ("Playing 1loop"). Shipping it means nobody has to make
// their own. ONELOOP_DISCORD_APP_ID replaces it, to try another one.
const discordAppID = "1569084103125508196"

// discordArt is where Discord fetches the presence images from: the work
// ring's animations as GIFs, drawn by TestDiscordArt into assets/discord.
// Discord takes any https image there and plays GIFs. The URL names the
// commit that added them, so every build finds its own images whatever
// branch it was built from: drawing them again means pointing it at the
// new commit.
const discordArt = "https://raw.githubusercontent.com/Feur-Inc/1loop/6cb9b3c9a5bd98dce442186833757a740d83d0e0/assets/"

// discordActivities is what the profile says each state of the work ring
// is doing, without the details (commands, files) that stay private.
var discordActivities = map[string]string{
	"think":    "Thinking",
	"write":    "Writing the answer",
	"wait":     "Waiting for a command", // one that lasts, or left in the background
	"call":     "Using a tool",
	"read":     "Reading files",
	"list":     "Looking through folders",
	"edit":     "Editing files",
	"bash":     "Running a command",
	"approval": "Waiting for the user",
	"compact":  "Compacting its context",
	"phone":    "Using the phone",
}

// syncPresence shows on Discord what the agent is doing, when the setting
// is on: the model and activity of the chat that started working last, for
// how long, and the chat and project only if the user chose to share them.
func (ui *desktopUI) syncPresence() {
	if !ui.discordPresence {
		if ui.presence != nil {
			ui.presence.Close()
			ui.presence = nil
		}
		return
	}
	if ui.presence == nil {
		appID := discordAppID
		if id := os.Getenv("ONELOOP_DISCORD_APP_ID"); id != "" {
			appID = id
		}
		if appID == "" {
			platform.Devf("discord", "no Discord application ID in this build")
		}
		ui.presence = discord.New(appID, func(format string, args ...any) { platform.Devf("discord", format, args...) })
	}
	ui.presence.Set(ui.presenceActivity())
}

func (ui *desktopUI) presenceActivity() *discord.Activity {
	activity := &discord.Activity{
		Buttons: []discord.Button{{Label: "Get 1loop", URL: "https://github.com/Feur-Inc/1loop"}},
	}
	var working *conversation
	running := 0
	for _, chat := range ui.chats {
		if chat.run == nil || chat.run.turn == nil {
			continue
		}
		running++
		if working == nil || chat.run.started.After(working.run.started) {
			working = chat
		}
	}
	if ui.loopyDoing.Running {
		running++
		if working == nil || ui.loopySince.After(working.run.started) {
			ui.loopyPresence(activity)
			if running > 1 {
				activity.State += fmt.Sprintf(" (+%d)", running-1)
			}
			return activity
		}
	}
	if working == nil {
		activity.Details = "Idle"
		activity.LargeImage, activity.LargeText = discordArt+"1loop-ring-on-black.png", "1loop"
		return activity
	}
	currentTurn := working.run.turn
	mark := currentTurn.mark
	if discordActivities[mark] == "" {
		mark = "think"
	}
	doing := discordActivities[mark]
	model := ui.modelName(working.Config)
	activity.Start = working.run.started
	activity.LargeImage = discordArt + "discord/" + mark + ".gif"
	activity.LargeText = doing + " · " + model
	activity.Details, activity.State = "Working with "+model, doing
	if ui.discordDetails {
		activity.Details = working.Title
		if project := filepath.Base(working.Config.Workspace); working.Config.Workspace != "" && project != "." {
			activity.Details = project + " · " + working.Title
		}
		if label := strings.TrimSuffix(currentTurn.label, "…"); label != "" && mark != "wait" { // the wait counts seconds
			activity.State = label
		}
	}
	if running > 1 {
		activity.State += fmt.Sprintf(" (+%d)", running-1)
	}
	return activity
}

// loopyPresence is what the profile says while Loopy works: what it does
// and with which model, and its own words for it only if the user chose to
// share the chat.
func (ui *desktopUI) loopyPresence(activity *discord.Activity) {
	doing := ui.loopyDoing
	mark := "think"
	switch {
	case doing.State == "waiting":
		mark = "approval"
	case doing.State == "typing":
		mark = "write"
	case doing.State == "reading" && !strings.HasPrefix(doing.Tool, "browser_"):
		mark = "read"
	case doing.State == "reading", doing.State == "copy":
		mark = "call"
	}
	what := discordActivities[mark]
	switch {
	case strings.HasPrefix(doing.Tool, "browser_") || doing.Tool == "loopy_inspect":
		what = "Browsing the web"
	case mark == "read":
		what = "Reading"
	}
	activity.Start = ui.loopySince
	activity.LargeImage = discordArt + "discord/" + mark + ".gif"
	activity.LargeText, activity.Details, activity.State = what+" · Loopy", "Working with Loopy", what
	if ui.loopyModel != "" {
		activity.LargeText, activity.Details = what+" · "+ui.loopyModel, "Loopy · "+ui.loopyModel
	}
	// Its other words speak to the user ("Waiting for you"): only a tool's
	// say what it does.
	if label := strings.TrimSuffix(doing.Label, "…"); ui.discordDetails && label != "" && (doing.State == "reading" || doing.State == "copy") {
		activity.State = label
	}
}

// noteLoopy keeps what the app's Loopy at at does for the Discord status,
// and reads the model it works with each time it starts.
func (ui *desktopUI) noteLoopy(at string, change loopy.Change) {
	started := change.Running && !ui.loopyDoing.Running
	ui.loopyDoing = change
	if started {
		ui.loopySince = time.Now()
	}
	ui.syncPresence()
	// A server's events start again with a change that only says to read it
	// all: whether it works is in its view.
	if !ui.discordPresence || !started && change.Version >= 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ui.ctx, 30*time.Second)
		defer cancel()
		client, err := ui.loopyClient(at)
		if err != nil {
			return
		}
		view, err := client.View(ctx)
		if err != nil {
			return
		}
		fyne.Do(func() {
			if at != ui.loopyServer() {
				return
			}
			ui.loopyModel = loopyModelName(view)
			if change.Version < 0 && ui.loopyDoing.Version < 0 {
				if view.Running {
					ui.loopySince = time.Now()
				}
				ui.loopyDoing = loopy.Change{Version: view.Version, State: view.State, Label: view.Label, Tool: view.Tool, Running: view.Running}
			}
			ui.syncPresence()
		})
	}()
}

// discordArtPeriod is the length of one loop of the presence GIFs: the
// thinking dots' breath, a little slower than the ring's turn on screen.
const discordArtPeriod = 1200 * time.Millisecond

// discordArtFrame draws the work ring in a state at a time of its loop, on
// a dark square: GIF pixels are opaque or not at all, so the ring's soft
// edges need a background.
func discordArtFrame(art *ringArt, state string, at time.Duration) *image.Paletted {
	px := art.px
	out := image.NewNRGBA(image.Rect(0, 0, px, px))
	draw.Draw(out, out.Bounds(), image.NewUniform(discordBackground), image.Point{}, draw.Src)
	layer := func(src image.Image, opacity float64) {
		mask := image.NewUniform(color.Alpha{A: uint8(math.Min(1, opacity)*255 + 0.5)})
		draw.DrawMask(out, out.Bounds(), src, image.Point{}, mask, image.Point{}, draw.Over)
	}
	t := float64(at) / float64(discordArtPeriod)
	angle := 360 * t
	layer(art.open(angle), 1)
	layer(art.dot(angle), 1)
	if state == "think" {
		for i, cx := range []string{"46", "60", "74"} {
			layer(art.icon("dot"+cx, 1), thinkOpacity(t-0.125*float64(i)))
		}
	} else {
		layer(art.icon(state, 1), 1)
	}
	paletted := image.NewPaletted(out.Bounds(), discordPalette())
	draw.Draw(paletted, out.Bounds(), out, image.Point{}, draw.Src)
	return paletted
}

var discordBackground = color.NRGBA{0x16, 0x16, 0x16, 0xff}

// discordPalette is the background blended towards the ring's amber and
// towards the icons' white: every colour the frames have.
func discordPalette() color.Palette {
	palette := color.Palette{discordBackground}
	blend := func(to color.NRGBA, steps int) {
		for i := 1; i <= steps; i++ {
			k := float64(i) / float64(steps)
			mix := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*k + 0.5) }
			palette = append(palette, color.NRGBA{mix(discordBackground.R, to.R), mix(discordBackground.G, to.G), mix(discordBackground.B, to.B), 0xff})
		}
	}
	blend(color.NRGBA{0xff, 0xb0, 0x00, 0xff}, 128)
	blend(color.NRGBA{0xf4, 0xf4, 0xf4, 0xff}, 127)
	return palette
}

// discordGIF is the loop of a state, 24 frames.
func discordGIF(state string, px int) *gif.GIF {
	art := newRingArt(px)
	art.brand, art.ink = true, brandHex // Discord shows the brand's amber
	const frames = 24
	animation := &gif.GIF{LoopCount: 0}
	for i := 0; i < frames; i++ {
		animation.Image = append(animation.Image, discordArtFrame(art, state, discordArtPeriod*time.Duration(i)/frames))
		animation.Delay = append(animation.Delay, int(discordArtPeriod/time.Millisecond/10/frames))
	}
	return animation
}
