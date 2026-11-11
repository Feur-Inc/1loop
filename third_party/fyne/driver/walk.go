package driver

import (
	"fyne.io/fyne/v2"
	intdriver "fyne.io/fyne/v2/internal/driver"
)

// WalkObjects calls each for obj and everything inside it, hidden objects
// and the insides of widgets already drawn included.
//
// 1loop: exported to repaint a window on a change of theme.
func WalkObjects(obj fyne.CanvasObject, each func(fyne.CanvasObject)) {
	intdriver.WalkCompleteObjectTree(obj, func(o fyne.CanvasObject, _, _ fyne.Position, _ fyne.Size) bool {
		each(o)
		return false
	}, nil)
}
