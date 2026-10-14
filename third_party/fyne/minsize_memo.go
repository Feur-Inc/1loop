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

// BeginMinSizeMemo starts remembering minimum sizes; EndMinSizeMemo stops.
func BeginMinSizeMemo() { minSizeMemo = make(map[CanvasObject]Size, 512) }

// EndMinSizeMemo forgets the remembered sizes and stops remembering.
func EndMinSizeMemo() { minSizeMemo = nil }

// ForgetMinSizes drops what was remembered: something was laid out.
func ForgetMinSizes() {
	if minSizeMemo != nil {
		minSizeMemo = make(map[CanvasObject]Size, 512)
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
