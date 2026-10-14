package driver

import "time"

// FrameTimes, when set, receives how long each part of a window's redraw
// took: checking minimum sizes, painting, and presenting the frame. It is
// called on the thread that draws. (1loop: for its benchmarks.)
var FrameTimes func(sizes, paint, present time.Duration)
