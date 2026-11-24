// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// The composer's controls never overlap nor leave the card, however narrow
// the chat beside the side panel (#429).
func TestComposerControlsNeverOverlap(t *testing.T) {
	test.NewApp()
	block := func(width float32) *canvas.Rectangle {
		rect := canvas.NewRectangle(color.Black)
		rect.SetMinSize(fyne.NewSize(width, 30))
		return rect
	}
	model := newRow("Opus 5.5 · Claude Code", nil, styleChip, nil)
	model.truncate = true
	approval, ring := block(95), block(26)
	settings := container.New(composerSettingsLayout{model: model}, approval, model, ring)
	steer, stop, send := block(70), block(36), block(36)
	actions := container.New(composerActionsLayout{}, steer, stop, send)
	controls := container.New(&composerControlsLayout{}, settings, actions)
	type span struct {
		name     string
		from, to float32
	}
	// The chat is never under chatLeast wide: 48 around the card, 20 inside.
	for width := float32(chatLeast - 68); width <= 760; width += 3 {
		controls.Resize(fyne.NewSize(width, 40))
		var spans []span
		add := func(name string, parent *fyne.Container, object fyne.CanvasObject) {
			if x := object.Position().X; x > -5000 {
				spans = append(spans, span{name, parent.Position().X + x, parent.Position().X + x + object.Size().Width})
			}
		}
		add("approval", settings, approval)
		add("model", settings, model)
		add("ring", settings, ring)
		add("steer", actions, steer)
		add("stop", actions, stop)
		add("send", actions, send)
		for i, s := range spans {
			if s.from < -0.5 || s.to > width+0.5 {
				t.Fatalf("at %v, %s is at %v-%v", width, s.name, s.from, s.to)
			}
			for _, other := range spans[i+1:] {
				if s.from < other.to-0.5 && other.from < s.to-0.5 {
					t.Fatalf("at %v, %s (%v-%v) and %s (%v-%v) overlap", width, s.name, s.from, s.to, other.name, other.from, other.to)
				}
			}
		}
		if stop.Position().X < -5000 || send.Position().X < -5000 || approval.Position().X < -5000 {
			t.Fatalf("at %v, Stop, Send or the approval mode is out of sight", width)
		}
	}
	if steer.Position().X < -5000 || ring.Position().X < -5000 {
		t.Fatal("Steer or the ring is hidden with all the room")
	}
}

// Side by side, the chat and the panel keep room for what they show; where
// both cannot, the panel takes it all.
func TestPanelLeavesTheChatItsRoom(t *testing.T) {
	for total := float32(200); total < 1800; total += 7 {
		for _, wanted := range []float32{0, 320, 840} {
			width := panelWidthWithin(wanted, total)
			if panelCovers(total) {
				if width != total {
					t.Fatalf("of %v, a covering panel takes %v", total, width)
				}
				continue
			}
			if width < panelLeast-0.5 || total-width < chatLeast-0.5 {
				t.Fatalf("of %v (wanted %v), the panel takes %v", total, wanted, width)
			}
		}
	}
}
