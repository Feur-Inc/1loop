package driver

import "fyne.io/fyne/v2"

// Recoloured, when set by the driver, tells it that only colours changed
// (1loop: a palette change, the fonts kept): the theme's icons are drawn
// again, the textures of the objects given (images and gradients drawn in
// other colours) are dropped, and every window is painted again, without
// the theme being set again. Setting it again refreshed every widget,
// which dropped every texture of the window: each text was drawn again
// and each picture resampled, for a change of accent. Called on the
// thread that draws.
var Recoloured func(objects []fyne.CanvasObject)
