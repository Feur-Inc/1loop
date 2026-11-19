package driver

import (
	"sync/atomic"
	"time"
)

// FrameTimes, when set, receives how long each part of a window's redraw
// took: checking minimum sizes, painting, and presenting the frame. It is
// called on the thread that draws. (1loop: for its benchmarks.)
var FrameTimes func(sizes, paint, present time.Duration)

// TurnHook, when set, is called at every turn of the driver's loop on the
// thread that draws, just before animations tick and windows may be drawn.
// (1loop: its frame hook, which was a never-ending animation that kept the
// animation runner busy.)
var TurnHook func()

// Refreshes counts the refreshes asked of objects (canvas.Refresh, which
// widgets reach too), whether or not the object was found in a window.
// (1loop: a window nothing was refreshed in has nothing new to show.)
var Refreshes atomic.Uint64

// KeepFonts, while true, has a theme set again keep the fonts and the text
// already shaped: only colours changed. (1loop: a change of accent set the
// theme again, and every text of the window was shaped again.)
var KeepFonts bool
