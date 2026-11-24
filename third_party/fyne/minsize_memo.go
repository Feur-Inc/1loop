package fyne

// 1loop: a canvas checks every visible object's minimum size before each
// frame, and a container's minimum size asks its children for theirs,
// which ask theirs: the same sizes were computed once per level of the
// tree, at every frame. During that pass each one is now computed once.
//
// The memory only lives for one pass, on the thread that draws, and is
// forgotten as soon as the pass lays anything out (a wrapping text's
// minimum size depends on its width).

var minSizeMemo map[CanvasObject]Size

// minSizeKept is the memory's map, emptied and used again from one pass
// to the next: a new one at each frame and each layout was most of a
// frame's allocations (#474).
var minSizeKept = make(map[CanvasObject]Size, 512)

// BeginMinSizeMemo starts remembering minimum sizes; EndMinSizeMemo stops.
func BeginMinSizeMemo() { minSizeMemo = minSizeKept }

// EndMinSizeMemo forgets the remembered sizes and stops remembering.
func EndMinSizeMemo() {
	forgetMinSizes(minSizeKept)
	minSizeMemo = nil
}

// ForgetMinSizes drops what was remembered: something was laid out.
func ForgetMinSizes() {
	if minSizeMemo != nil {
		forgetMinSizes(minSizeMemo)
	}
}

func forgetMinSizes(memo map[CanvasObject]Size) {
	for object := range memo { // the compiler makes this a map clear
		delete(memo, object)
	}
}

// MemoMinSize returns object's remembered minimum size, or computes it.
func MemoMinSize(object CanvasObject, compute func() Size) Size {
	if minSizeMemo == nil {
		return compute()
	}
	if size, ok := minSizeMemo[object]; ok {
		return size
	}
	size := compute()
	minSizeMemo[object] = size
	return size
}
