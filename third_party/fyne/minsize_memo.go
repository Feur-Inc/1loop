package fyne

// 1loop: a canvas checks every visible object's minimum size before each
// frame, and a container's minimum size asks its children for theirs,
// which ask theirs: the same sizes were computed once per level of the
// tree, at every frame. During that pass each one is now computed once.
//
// The memory only lives for one pass, on the thread that draws, and is
// forgotten as soon as anything changes size: a wrapping text's minimum
// size depends on its width. A box lays a child out with the minimum size
// it had at its old width, then resizes it; kept after that, the old size
// left a re-wrapped paragraph too high (gaps between rows, #497) or too
// low (rows drawn over each other, #440).

type memoSize struct {
	size Size
	era  uint64
}

var (
	minSizeMemo map[CanvasObject]memoSize
	memoEra     uint64 // sizes remembered in an earlier era are forgotten
)

// minSizeKept is the memory's map, emptied and used again from one pass
// to the next: a new one at each frame and each layout was most of a
// frame's allocations (#474).
var minSizeKept = make(map[CanvasObject]memoSize, 512)

// BeginMinSizeMemo starts remembering minimum sizes; EndMinSizeMemo stops.
func BeginMinSizeMemo() { minSizeMemo = minSizeKept }

// EndMinSizeMemo forgets the remembered sizes and stops remembering.
func EndMinSizeMemo() {
	for object := range minSizeKept { // the compiler makes this a map clear
		delete(minSizeKept, object)
	}
	minSizeMemo = nil
}

// ForgetMinSizes drops what was remembered: something was laid out or
// resized. It costs nothing when no pass is remembering.
func ForgetMinSizes() {
	if minSizeMemo != nil {
		memoEra++
	}
}

// RememberedMinSize is object's minimum size remembered in this pass, if
// any. RememberMinSize remembers it while a pass is on. (Both replace a
// MemoMinSize that took the computation as a func: the method value passed
// was allocated at every call, an allocation per container per frame.)
func RememberedMinSize(object CanvasObject) (Size, bool) {
	if minSizeMemo == nil {
		return Size{}, false
	}
	if known, ok := minSizeMemo[object]; ok && known.era == memoEra {
		return known.size, true
	}
	return Size{}, false
}

// RememberMinSize: see RememberedMinSize.
func RememberMinSize(object CanvasObject, size Size) {
	if minSizeMemo != nil {
		minSizeMemo[object] = memoSize{size, memoEra}
	}
}
