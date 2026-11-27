// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"cmp"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"1loop/internal/discord"
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

type presenceWork struct {
	title     string
	workspace string
	scratch   bool
	model     string
	mark      string
	label     string
	started   time.Time
	rich      bool // true when mark/label came from live turn state rather than a chat list summary
}

func (ui *desktopUI) presenceStartFor(key string, now time.Time) time.Time {
	if ui.presenceStarted == nil {
		ui.presenceStarted = map[string]time.Time{}
	}
	if started, ok := ui.presenceStarted[key]; ok {
		return started
	}
	ui.presenceStarted[key] = now
	return now
}

func (ui *desktopUI) presenceWorkList() []presenceWork {
	now := time.Now()
	var list []presenceWork
	seenRemoteKeys := map[string]bool{}

	for _, chat := range ui.chats {
		if chat.run == nil || chat.run.turn == nil {
			continue
		}
		list = append(list, presenceWork{
			title:     chat.Title,
			workspace: chat.Config.Workspace,
			scratch:   chat.Config.InScratch(),
			model:     ui.modelName(chat.Config),
			mark:      chat.run.turn.mark,
			label:     chat.run.turn.label,
			started:   chat.run.started,
			rich:      true,
		})
	}

	seenServers := map[string]bool{}
	addRemote := func(remote *remoteDesktop) {
		if remote == nil || remote.client == nil {
			return
		}
		addr := remote.client.address.URL
		if seenServers[addr] {
			return
		}
		seenServers[addr] = true

		addedCurrent := false
		if remote.current != "" && remote.meta.Running {
			addedCurrent = true
			key := addr + "\x00" + remote.current
			seenRemoteKeys[key] = true
			title, workspace, scratch := "", "", false
			for _, chat := range remote.chats {
				if chat.ID == remote.current {
					title, workspace, scratch = chat.Title, chat.Workspace, chat.Scratch
					break
				}
			}
			if conv := remote.convs[remote.current]; conv != nil {
				if title == "" {
					title = conv.Title
				}
				if workspace == "" {
					workspace = conv.Config.Workspace
				}
			}
			mark := remote.meta.Mark
			if remote.meta.Approval != nil || remote.meta.Question != nil {
				mark = "approval"
			}
			model := ""
			if m := cmp.Or(remote.meta.Model, remote.model); m != "" {
				model, _ = remote.modelChip(m, "")
			}
			list = append(list, presenceWork{
				title:     title,
				workspace: workspace,
				scratch:   scratch,
				model:     model,
				mark:      mark,
				label:     remote.meta.Label,
				started:   ui.presenceStartFor(key, now),
				rich:      true,
			})
		}

		defaultModel := ""
		if remote.model != "" {
			defaultModel, _ = remote.modelChip(remote.model, "")
		}
		for _, chat := range remote.chats {
			if !chat.Running || (addedCurrent && chat.ID == remote.current) {
				continue
			}
			key := addr + "\x00" + chat.ID
			seenRemoteKeys[key] = true
			mark := "think"
			if chat.Blocked {
				mark = "approval"
			}
			list = append(list, presenceWork{
				title:     chat.Title,
				workspace: chat.Workspace,
				scratch:   chat.Scratch,
				model:     defaultModel,
				mark:      mark,
				started:   ui.presenceStartFor(key, now),
			})
		}
	}

	addRemote(ui.server)
	for _, view := range ui.allPanes() {
		addRemote(view.server)
	}
	for _, link := range ui.links {
		if !link.answered || seenServers[link.address.URL] {
			continue
		}
		seenServers[link.address.URL] = true
		for _, chat := range link.listing.chats {
			if !chat.Running {
				continue
			}
			key := link.address.URL + "\x00" + chat.ID
			seenRemoteKeys[key] = true
			mark := "think"
			if chat.Blocked {
				mark = "approval"
			}
			list = append(list, presenceWork{
				title:     chat.Title,
				workspace: chat.Workspace,
				scratch:   chat.Scratch,
				mark:      mark,
				started:   ui.presenceStartFor(key, now),
			})
		}
	}

	// Drop start times for remote chats that are no longer running.
	for key := range ui.presenceStarted {
		if !seenRemoteKeys[key] {
			delete(ui.presenceStarted, key)
		}
	}
	return list
}

func (ui *desktopUI) presenceActivity() *discord.Activity {
	activity := &discord.Activity{
		Buttons: []discord.Button{{Label: "Get 1loop", URL: "https://github.com/Feur-Inc/1loop"}},
	}
	work := ui.presenceWorkList()
	if len(work) == 0 {
		activity.Details = "Idle"
		activity.LargeImage, activity.LargeText = discordArt+"1loop-ring-on-black.png", "1loop"
		return activity
	}
	chosen := work[0]
	for _, candidate := range work[1:] {
		if candidate.started.After(chosen.started) || (candidate.started.Equal(chosen.started) && candidate.rich && !chosen.rich) {
			chosen = candidate
		}
	}
	mark := chosen.mark
	if discordActivities[mark] == "" {
		mark = "think"
	}
	doing := discordActivities[mark]
	activity.Start = chosen.started
	activity.LargeImage = discordArt + "discord/" + mark + ".gif"
	if chosen.model != "" {
		activity.LargeText = doing + " · " + chosen.model
		activity.Details = "Working with " + chosen.model
	} else {
		activity.LargeText = doing
		activity.Details = "Working on a server"
	}
	activity.State = doing
	if ui.discordDetails {
		if chosen.title != "" {
			activity.Details = chosen.title
		}
		if !chosen.scratch && chosen.workspace != "" {
			if project := path.Base(filepath.ToSlash(chosen.workspace)); project != "" && project != "." && project != "/" {
				if chosen.title != "" {
					activity.Details = project + " · " + chosen.title
				} else {
					activity.Details = project
				}
			}
		}
		if label := strings.TrimSuffix(chosen.label, "…"); label != "" && mark != "wait" { // the wait counts seconds
			activity.State = label
		}
	}
	if len(work) > 1 {
		activity.State += fmt.Sprintf(" (+%d)", len(work)-1)
	}
	return activity
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
