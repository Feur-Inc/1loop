package glfw

import (
	"image/color"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

func TestDamageAreaReachesImageAndText(t *testing.T) {
	area := damageArea{pos: fyne.NewPos(100, 500), size: fyne.NewSize(24, 24)}

	nearImg := canvas.NewImageFromImage(nil)
	nearImg.Resize(fyne.NewSize(20, 20))
	if !area.reaches(nearImg, fyne.NewPos(102, 502)) {
		t.Fatal("expected overlapping image to reach damage area")
	}
	if area.reaches(nearImg, fyne.NewPos(10, 10)) {
		t.Fatal("expected distant image to be culled")
	}

	txt := canvas.NewText("hello", color.White)
	txt.TextSize = 14
	txt.Resize(fyne.NewSize(200, 18))
	if !area.reaches(txt, fyne.NewPos(10, 505)) {
		t.Fatal("expected text in same vertical band to reach damage area")
	}
	if area.reaches(txt, fyne.NewPos(10, 100)) {
		t.Fatal("expected text far above damage area to be culled")
	}
}

func TestRetentionAllowsPartialWithinGap(t *testing.T) {
	t0 := time.Unix(1000, 0)
	r := retention{last: t0, partial: false, held: true}
	if !r.retains(t0.Add(20*time.Millisecond), true) {
		t.Fatal("expected held retained frame to stay retained for a partial frame within retainGap")
	}
	if r.retains(t0.Add(20*time.Millisecond), false) {
		t.Fatal("expected back-to-back full frames within retainGap to skip retained blit")
	}
	if !r.retains(t0.Add(retainGap), false) {
		t.Fatal("expected full frame after retainGap to be retained")
	}
}
