package driver

import "sync/atomic"

// PartialFrames counts the frames that painted only the part of a window
// that changed, the rest kept from the frame before. (1loop: a blinking
// caret repainted the whole window.)
var PartialFrames atomic.Uint64
