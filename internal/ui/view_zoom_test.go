// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"math"
	"testing"

	"fyne.io/fyne/v2"
)

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.01 }

func TestViewZoomKeepsThePointUnderTheCursor(t *testing.T) {
	size := fyne.NewSize(400, 300)
	var zoom viewZoom
	at := fyne.NewPos(100, 60)
	zoom.zoomAt(2, at, size)
	if !zoom.active() || zoom.label() != "200%" {
		t.Fatalf("zoom = %+v, label %s", zoom, zoom.label())
	}
	if got := zoom.at(at); !near(got.X, at.X) || !near(got.Y, at.Y) {
		t.Fatalf("the point moved to %v", got)
	}
	zoom.zoomAt(1.5, fyne.NewPos(300, 200), size)
	if got := zoom.at(fyne.NewPos(200, 130)); !near(got.X, 300) || !near(got.Y, 200) {
		// (300,200) on screen was (200,130) unzoomed at 200% from (100,60)
		t.Fatalf("second zoom moved the point to %v", got)
	}
}

func TestViewZoomStaysWithinThePicture(t *testing.T) {
	size := fyne.NewSize(400, 300)
	var zoom viewZoom
	zoom.zoomAt(100, fyne.NewPos(0, 0), size)
	if zoom.scale != viewZoomMax {
		t.Fatalf("scale = %v, want the maximum", zoom.scale)
	}
	zoom.pan(fyne.NewDelta(500, 500), size)
	if zoom.x != 0 || zoom.y != 0 {
		t.Fatalf("panned past the top left: %+v", zoom)
	}
	zoom.pan(fyne.NewDelta(-1e6, -1e6), size)
	if !near(zoom.x, 400-400*viewZoomMax) || !near(zoom.y, 300-300*viewZoomMax) {
		t.Fatalf("panned past the bottom right: %+v", zoom)
	}
	zoom.zoomAt(0.01, fyne.NewPos(10, 10), size)
	if zoom.active() || zoom.x != 0 || zoom.y != 0 {
		t.Fatalf("zooming out below 100%% left %+v", zoom)
	}
}

func TestViewZoomUnzoomedIsTheAgentsView(t *testing.T) {
	var zoom viewZoom
	zoom.pan(fyne.NewDelta(40, 40), fyne.NewSize(400, 300))
	if zoom.active() || zoom.at(fyne.NewPos(5, 7)) != fyne.NewPos(5, 7) || zoom.sized(fyne.NewSize(3, 4)) != fyne.NewSize(3, 4) {
		t.Fatalf("an unzoomed view moved: %+v", zoom)
	}
}

func TestViewZoomFollowsTheAgentWhenItNavigates(t *testing.T) {
	size := fyne.NewSize(400, 300)
	var zoom viewZoom
	zoom.follow("https://example.com/")
	zoom.zoomAt(3, fyne.NewPos(200, 150), size)
	if zoom.follow("https://example.com/") || !zoom.active() {
		t.Fatal("the same page reset the view")
	}
	if !zoom.follow("https://example.com/next") || zoom.active() {
		t.Fatal("a navigation kept the view zoomed")
	}
	zoom.zoomAt(2, fyne.NewPos(0, 0), size)
	zoom.reset()
	if zoom.active() || zoom.label() != "100%" {
		t.Fatalf("reset left %+v", zoom)
	}
}

func TestWheelFactor(t *testing.T) {
	if wheelFactor(0) != 1 || wheelFactor(10) <= 1 || wheelFactor(-10) >= 1 {
		t.Fatal("wheel up zooms in, down zooms out")
	}
	if !near(wheelFactor(10)*wheelFactor(-10), 1) {
		t.Fatal("a notch in and out is no zoom")
	}
}

func TestWheelFactorIgnoresAcceleration(t *testing.T) {
	if wheelFactor(25*125) != wheelFactor(25) || wheelFactor(25) > 1.2 {
		t.Fatalf("an accelerated notch zooms %v, a notch %v", wheelFactor(25*125), wheelFactor(25))
	}
}

func TestFitFrameKeepsTheFramesProportions(t *testing.T) {
	// A 754 × 471 frame shown while the page is already 754 × 770: drawn
	// as it is, letterboxed, not stretched to the page's new proportions.
	pos, rendered := fitFrame(fyne.NewSize(760, 700), fyne.NewSize(754, 471))
	if !near(rendered.Width/rendered.Height, 754.0/471) || !near(rendered.Width, 760) {
		t.Fatalf("rendered %v", rendered)
	}
	if !near(pos.X, 0) || !near(pos.Y, (700-rendered.Height)/2) {
		t.Fatalf("not centred: %v", pos)
	}
}
