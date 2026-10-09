package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	xdraw "golang.org/x/image/draw"
)

// ---------------------------------------------------------------------------
// chatEntry is the prompt: Enter sends (or queues while a task runs),
// Ctrl+Enter steers the running task, Shift+Enter starts a new line. It
// grows with its text, up to a few lines, like a web chat input.

type chatEntry struct {
	widget.Entry
	send     func() // Enter
	steer    func() // Ctrl+Enter
	escape   func() // Escape
	onFocus  func(bool)
	shortcut func(fyne.Shortcut) bool
	paste    func() bool // image-aware paste (Ctrl+V), true when it handled the key
	rows     int
}

const promptMaxRows = 10

func newChatEntry() *chatEntry {
	e := &chatEntry{rows: 1}
	e.MultiLine = true
	e.Wrapping = fyne.TextWrapWord
	e.ExtendBaseWidget(e)
	e.SetMinRowsVisible(1)
	e.OnChanged = func(string) { e.fit() }
	return e
}

// fit sizes the entry to its wrapped text so the composer grows as the
// prompt does, then scrolls past promptMaxRows.
func (e *chatEntry) fit() {
	width := e.Size().Width - 2*theme.InnerPadding() - 4
	if width <= 0 {
		return
	}
	rows := 0
	for _, line := range strings.Split(e.Text, "\n") {
		measured := fyne.MeasureText(line, theme.TextSize(), fyne.TextStyle{}).Width
		rows += int(math.Max(1, math.Ceil(float64(measured/width))))
	}
	rows = int(math.Min(math.Max(1, float64(rows)), promptMaxRows))
	if rows != e.rows {
		e.rows = rows
		e.SetMinRowsVisible(rows)
	}
}

func (e *chatEntry) Resize(size fyne.Size) {
	e.Entry.Resize(size)
	e.fit()
}

// The composer card shows focus itself, so the entry's own outline is hidden.
// Its width cannot be themed to zero: Fyne sizes the text cursor with it.
func (e *chatEntry) CreateRenderer() fyne.WidgetRenderer {
	return &borderlessRenderer{WidgetRenderer: e.Entry.CreateRenderer()}
}

type borderlessRenderer struct{ fyne.WidgetRenderer }

func (r *borderlessRenderer) hide() {
	for _, o := range r.WidgetRenderer.Objects() {
		if rect, ok := o.(*canvas.Rectangle); ok && rect.StrokeWidth > 0 {
			rect.StrokeColor = color.Transparent
		}
	}
}

func (r *borderlessRenderer) Refresh() { r.WidgetRenderer.Refresh(); r.hide() }
func (r *borderlessRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	r.hide()
}

// App shortcuts must work while typing: Fyne hands every shortcut to the
// focused entry, which ignores the ones it does not know. Ctrl+Enter also
// arrives here, as a shortcut rather than a key.
func (e *chatEntry) TypedShortcut(s fyne.Shortcut) {
	if c, ok := s.(*desktop.CustomShortcut); ok && c.Modifier == fyne.KeyModifierControl &&
		(c.KeyName == fyne.KeyReturn || c.KeyName == fyne.KeyEnter) {
		e.steer()
		return
	}
	if _, ok := s.(*fyne.ShortcutPaste); ok && e.paste != nil && e.paste() {
		return // an image was staged as an attachment
	}
	if e.shortcut != nil && e.shortcut(s) {
		return
	}
	e.Entry.TypedShortcut(s)
}

func (e *chatEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape && e.escape != nil {
		e.escape()
		return
	}
	if key.Name == fyne.KeyReturn || key.Name == fyne.KeyEnter {
		shift := false
		if driver, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
			shift = driver.CurrentKeyModifiers()&fyne.KeyModifierShift != 0
		}
		if !shift {
			e.send()
			return
		}
	}
	e.Entry.TypedKey(key) // Shift+Enter: a new line
}

func (e *chatEntry) FocusGained() {
	e.Entry.FocusGained()
	if e.onFocus != nil {
		e.onFocus(true)
	}
}

func (e *chatEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onFocus != nil {
		e.onFocus(false)
	}
}

// attachButtonLayout puts the "+" button at the left of the prompt, centred
// on its first line however many lines the prompt grows to.
type attachButtonLayout struct{}

const attachButtonGap = 4.0 // between the "+" and the text field

func (attachButtonLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	input, btn := objects[0], objects[1]
	return fyne.NewSize(btn.MinSize().Width+attachButtonGap+input.MinSize().Width,
		fyne.Max(input.MinSize().Height, btn.MinSize().Height))
}

func (attachButtonLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	input, btn := objects[0], objects[1]
	btnSize := btn.MinSize()
	btn.Resize(btnSize)
	line := fyne.MeasureText("Ag", theme.TextSize(), fyne.TextStyle{}).Height
	btn.Move(fyne.NewPos(0, fyne.Max(0, theme.InnerPadding()+line/2-btnSize.Height/2)))
	reserved := btnSize.Width + attachButtonGap
	input.Resize(fyne.NewSize(size.Width-reserved, size.Height))
	input.Move(fyne.NewPos(reserved, 0))
}

// ---------------------------------------------------------------------------
// Image attachments: pasted from the clipboard or picked from files, shown
// as thumbnails above the prompt, and sent to the model as data URLs.

var imageMime = regexp.MustCompile(`image/[a-z0-9.+-]+`)

func imageClipboardData() ([]byte, error) {
	// Fyne's clipboard is text-only, so images are read from the native
	// clipboard with wl-paste (Wayland) or xclip (X11).
	missing := fmt.Errorf("No image in the clipboard")
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		out, err := exec.Command("wl-paste", "--list-types").Output()
		if err != nil {
			return nil, missing
		}
		for _, raw := range strings.Split(string(out), "\n") {
			if mime := imageMime.FindString(strings.ToLower(raw)); mime != "" {
				// --no-newline keeps binary data intact.
				data, err := exec.Command("wl-paste", "--no-newline", "-t", mime).Output()
				if err != nil || len(bytes.TrimSpace(data)) == 0 {
					return nil, missing
				}
				return data, nil
			}
		}
		return nil, missing
	}
	data, err := exec.Command("xclip", "-selection", "clipboard", "-t", "image/png", "-o").Output()
	if err != nil {
		return nil, missing
	}
	return data, nil
}

func decodeImage(raw []byte) (*image.NRGBA, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("Unsupported image format")
	}
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	return out, nil
}

// pasteFromClipboard stages an image from the system clipboard as an
// attachment. It reports true when it handled the key; a text-only
// clipboard is left to the entry's own paste so Ctrl+V behaves normally.
func (u *desktopUI) pasteFromClipboard() bool {
	raw, err := imageClipboardData()
	if err != nil {
		return false
	}
	img, err := decodeImage(raw)
	if err != nil {
		u.notice("Paste", err.Error())
		return true
	}
	u.attachImage(img)
	return true
}

// pickImages attaches pictures chosen in the desktop's file chooser.
func (u *desktopUI) pickImages() {
	u.pick(pickRequest{title: "Attach images", start: u.cfg.Workspace, multiple: true, filterName: "Images",
		extensions: []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"}}, func(paths []string) {
		for _, path := range paths {
			f, err := os.Open(path)
			if err != nil {
				u.notice("Image", err.Error())
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(f, 25<<20))
			f.Close()
			if err != nil {
				u.notice("Image", err.Error())
				continue
			}
			img, err := decodeImage(raw)
			if err != nil {
				u.notice("Image", path+": "+err.Error())
				continue
			}
			u.attachImage(img)
		}
	})
}

// attachImage keeps a picture, as PNG, until the next message.
func (u *desktopUI) attachImage(img *image.NRGBA) {
	bounds := img.Bounds()
	// Big pictures cost tokens: cap the longest side at 1536 px.
	if side := fyne.Max(float32(bounds.Dx()), float32(bounds.Dy())); side > 1536 {
		scale := 1536.0 / side
		w, h := int(float32(bounds.Dx())*scale+0.5), int(float32(bounds.Dy())*scale+0.5)
		smaller := image.NewNRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(smaller, smaller.Bounds(), img, bounds, xdraw.Src, nil)
		img = smaller
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		u.notice("Image", err.Error())
		return
	}
	u.attachments = append(u.attachments, ImagePart{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(buf.Bytes())})
	u.renderAttachments()
}

func (u *desktopUI) renderAttachments() {
	u.attachRow.RemoveAll()
	for i, part := range u.attachments {
		i := i
		raw, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			continue
		}
		view := canvas.NewImageFromImage(img)
		view.FillMode = canvas.ImageFillContain
		view.SetMinSize(fyne.NewSize(64, 64))
		remove := newRow("", iconClose, styleWindow, func() {
			u.attachments = append(u.attachments[:i], u.attachments[i+1:]...)
			u.renderAttachments()
		})
		u.attachRow.Add(container.NewStack(surface(view, colCode, colBorderSoft, 8, 0), container.NewBorder(nil, nil, nil, remove)))
	}
	if len(u.attachRow.Objects) == 0 {
		u.attachTray.Hide()
	} else {
		u.attachTray.Show()
	}
}

// takeAttachments hands the staged pictures to a message and clears them.
func (u *desktopUI) takeAttachments() []ImagePart {
	images := u.attachments
	u.attachments = nil
	if len(images) > 0 {
		u.renderAttachments()
	}
	return images
}

// ---------------------------------------------------------------------------
// While a task runs, Enter queues the message: it becomes the next turn once
// the model has answered. Ctrl+Enter steers instead: the message joins the
// running task at the next gap between two model calls (after the current
// tool calls). Both wait, per chat, in a tray above the composer, where they
// can be reordered, edited in the prompt, sent at once or dropped.

func (u *desktopUI) takeSteer(chat *conversation) []message {
	out := chat.steer
	chat.steer = nil
	if len(out) > 0 && chat == u.current {
		u.renderQueue()
	}
	return out
}

func (u *desktopUI) takeNext(chat *conversation) (message, bool) {
	if len(chat.queue) == 0 {
		return message{}, false
	}
	next := chat.queue[0]
	chat.queue = chat.queue[1:]
	if chat == u.current {
		u.renderQueue()
	}
	return next, true
}

// lane is one of a chat's two waiting lists.
func lane(chat *conversation, steer bool) *[]message {
	if steer {
		return &chat.steer
	}
	return &chat.queue
}

func (u *desktopUI) moveQueued(index, delta int) {
	queue := u.current.queue
	to := index + delta
	if index < 0 || to < 0 || to >= len(queue) {
		return
	}
	queue[index], queue[to] = queue[to], queue[index]
	u.renderQueue()
}

func (u *desktopUI) dropQueued(steer bool, index int) {
	waiting := lane(u.current, steer)
	if index < 0 || index >= len(*waiting) {
		return
	}
	*waiting = append((*waiting)[:index], (*waiting)[index+1:]...)
	u.renderQueue()
}

// sendQueuedNow steers a queued message into the running task, or sends it
// right away when nothing runs.
func (u *desktopUI) sendQueuedNow(index int) {
	chat := u.current
	if index < 0 || index >= len(chat.queue) {
		return
	}
	m := chat.queue[index]
	chat.queue = append(chat.queue[:index], chat.queue[index+1:]...)
	if chat.run != nil {
		chat.steer = append(chat.steer, m)
		u.renderQueue()
		return
	}
	u.renderQueue()
	u.start(m)
}

func (u *desktopUI) clearQueue() {
	if u.current != nil {
		u.current.queue, u.current.steer = nil, nil
	}
	u.renderQueue()
}

// renderQueue shows the open chat's waiting messages.
func (u *desktopUI) renderQueue() {
	u.queueList.RemoveAll()
	chat := u.current
	if chat == nil || len(chat.steer)+len(chat.queue) == 0 {
		u.queueTray.Hide()
		return
	}
	for i, m := range chat.steer {
		u.queueList.Add(u.queueRow(true, i, m))
	}
	for i, m := range chat.queue {
		u.queueList.Add(u.queueRow(false, i, m))
	}
	label := fmt.Sprintf("%d queued", len(chat.queue))
	switch {
	case len(chat.queue) == 0:
		label = "Steering the current task"
	case len(chat.steer) > 0:
		label += fmt.Sprintf(" · %d steering", len(chat.steer))
	}
	if chat.run == nil {
		label += " · press Enter to send"
	}
	u.queueTitle.SetText(label)
	u.queueTray.Show()
}

// queueRow shows one waiting message; its actions appear on hover.
func (u *desktopUI) queueRow(steer bool, index int, m message) fyne.CanvasObject {
	text := strings.Join(strings.Fields(m.Content), " ")
	if text == "" {
		text = "[image]"
	} else if len(m.Images) > 0 {
		text += fmt.Sprintf("  [+%d image]", len(m.Images))
	}
	var badge fyne.CanvasObject
	if steer {
		arrow := canvas.NewImageFromResource(tinted(iconArrowTurn, colAmber))
		arrow.SetMinSize(fyne.NewSquareSize(16))
		badge = container.NewCenter(container.NewGridWrap(fyne.NewSquareSize(20), container.NewCenter(arrow)))
	} else {
		dot := canvas.NewCircle(colBorder)
		number := textLine(fmt.Sprint(index+1), 11, colMuted)
		number.Alignment = fyne.TextAlignCenter
		badge = container.NewCenter(container.NewStack(container.NewGridWrap(fyne.NewSquareSize(20), dot), container.NewCenter(number)))
	}
	label := newRow(text, nil, styleChip, func() { u.editQueued(steer, index) })
	label.size = 13.5
	label.truncate = true
	tips := map[*tapRow]string{}
	var actions []*tapRow
	action := func(icon fyne.Resource, tip string, run func()) {
		a := newRow("", icon, styleWindow, run)
		a.alpha = 0
		tips[a] = tip
		actions = append(actions, a)
	}
	if !steer {
		if index > 0 {
			action(iconChevronUp, "Move up", func() { u.moveQueued(index, -1) })
		}
		if index < len(u.current.queue)-1 {
			action(iconChevron, "Move down", func() { u.moveQueued(index, +1) })
		}
		action(iconArrowTurn, "Send now, at the next step", func() { u.sendQueuedNow(index) })
	}
	action(iconRename, "Edit in the prompt", func() { u.editQueued(steer, index) })
	action(iconClose, "Remove", func() { u.dropQueued(steer, index) })
	bar := container.New(layout.NewCustomPaddedHBoxLayout(0))
	for _, a := range actions {
		bar.Add(a)
	}
	hover := canvas.NewRectangle(color.Transparent)
	hover.CornerRadius = 8
	row := container.NewStack(hover, padded(0, 0, 4, 2, container.NewBorder(nil, nil, padded(0, 0, 0, 4, badge), bar, label)))
	revealOnHover(label, hover, actions, tips, u.tips)
	return row
}

// revealOnHover fades a row's actions in while the pointer is over the row
// or one of its actions (each is its own hover target, so both count).
func revealOnHover(label *tapRow, hover *canvas.Rectangle, actions []*tapRow, tips map[*tapRow]string, layer *tipLayer) {
	inside := map[*tapRow]bool{}
	anims := make([]*fyne.Animation, len(actions))
	var shown bool
	var later *time.Timer
	update := func() {
		in := false
		for _, v := range inside {
			in = in || v
		}
		if later != nil {
			later.Stop()
		}
		apply := func() {
			if in == shown {
				return
			}
			shown = in
			target := float32(0)
			hover.FillColor = color.Transparent
			if in {
				target = 1
				hover.FillColor = colHover
			}
			hover.Refresh()
			for i, a := range actions {
				fadeRow(a, target, &anims[i])
			}
		}
		if in {
			apply()
			return
		}
		// A short grace lets the pointer cross the gap between two targets.
		later = time.AfterFunc(80*time.Millisecond, func() { fyne.Do(apply) })
	}
	label.onHover = func(in bool) { inside[label] = in; update() }
	for _, a := range actions {
		a := a
		a.onHover = func(in bool) {
			inside[a] = in
			update()
			if in {
				layer.show(a, tips[a])
			} else {
				layer.hide()
			}
		}
	}
}

// queueEdit is a waiting message taken back into the prompt to be edited.
// It leaves the queue meanwhile, so the running task cannot send it half
// edited; Enter puts it back in its place, Escape restores it unchanged.
type queueEdit struct {
	chat     *conversation
	steer    bool
	index    int
	original message
	draft    string // what the prompt held before
	images   []ImagePart
}

func (u *desktopUI) editQueued(steer bool, index int) {
	u.finishEdit(false)
	chat := u.current
	waiting := lane(chat, steer)
	if index < 0 || index >= len(*waiting) {
		return
	}
	m := (*waiting)[index]
	*waiting = append((*waiting)[:index], (*waiting)[index+1:]...)
	u.editing = &queueEdit{chat: chat, steer: steer, index: index, original: m, draft: u.prompt.Text, images: u.attachments}
	u.prompt.SetText(m.Content)
	lines := strings.Split(m.Content, "\n")
	u.prompt.CursorRow, u.prompt.CursorColumn = len(lines)-1, len([]rune(lines[len(lines)-1]))
	u.prompt.Refresh()
	u.attachments = append([]ImagePart(nil), m.Images...)
	u.renderAttachments()
	u.editBar.Show()
	u.renderQueue()
	u.window.Canvas().Focus(u.prompt)
}

// finishEdit puts the message back in its place: as edited when commit is
// set (dropped if emptied), unchanged otherwise; the prompt gets its draft
// back.
func (u *desktopUI) finishEdit(commit bool) {
	e := u.editing
	if e == nil {
		return
	}
	u.editing = nil
	m := e.original
	if commit {
		m.Content, m.Images = strings.TrimSpace(u.prompt.Text), u.attachments
	}
	if m.Content != "" || len(m.Images) > 0 {
		waiting := lane(e.chat, e.steer)
		at := min(e.index, len(*waiting))
		*waiting = append((*waiting)[:at], append([]message{m}, (*waiting)[at:]...)...)
	}
	u.prompt.SetText(e.draft)
	u.attachments = e.images
	u.renderAttachments()
	u.editBar.Hide()
	u.renderQueue()
}

// ---------------------------------------------------------------------------
// contextRing shows how much of the model's window the conversation fills:
// amber for what is used, white for what is left. Hovering gives the figures.

type contextRing struct {
	widget.BaseWidget
	used, window int
	tips         *tipLayer
	track, fill  *canvas.Arc
}

func newContextRing(tips *tipLayer) *contextRing {
	r := &contextRing{tips: tips}
	r.track = canvas.NewArc(0, 360, 0.76, colText)
	r.fill = canvas.NewArc(0, 0, 0.76, colAmber)
	r.ExtendBaseWidget(r)
	return r
}

func (r *contextRing) set(used, window int) {
	r.used, r.window = used, window
	frac := float32(0)
	if window > 0 {
		frac = fyne.Min(1, float32(used)/float32(window))
	}
	r.fill.EndAngle = 360 * frac
	r.fill.Refresh()
	if r.tips.anchor == r {
		r.tips.place(r, r.describe())
	}
}

func (r *contextRing) describe() string {
	if r.window <= 0 {
		return "Context usage\nAvailable once a model is selected"
	}
	percent := r.used * 100 / r.window
	if percent > 100 {
		percent = 100
	}
	return fmt.Sprintf("%d%% of the context used\n%s of %s tokens", percent, tokens(r.used), tokens(r.window))
}

func tokens(n int) string {
	switch {
	case n >= 1000000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprint(n)
}

func (r *contextRing) MouseIn(*desktop.MouseEvent)    { r.tips.show(r, r.describe()) }
func (r *contextRing) MouseMoved(*desktop.MouseEvent) {}
func (r *contextRing) MouseOut()                      { r.tips.hide() }

func (r *contextRing) CreateRenderer() fyne.WidgetRenderer { return &contextRingRenderer{r} }

type contextRingRenderer struct{ r *contextRing }

const contextRingSize = 16

func (rr *contextRingRenderer) MinSize() fyne.Size { return fyne.NewSquareSize(contextRingSize + 12) }
func (rr *contextRingRenderer) Layout(size fyne.Size) {
	at := fyne.NewPos((size.Width-contextRingSize)/2, (size.Height-contextRingSize)/2)
	for _, arc := range []*canvas.Arc{rr.r.track, rr.r.fill} {
		arc.Move(at)
		arc.Resize(fyne.NewSquareSize(contextRingSize))
	}
}
func (rr *contextRingRenderer) Refresh() {
	rr.r.track.Refresh()
	rr.r.fill.Refresh()
}
func (rr *contextRingRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{rr.r.track, rr.r.fill}
}
func (rr *contextRingRenderer) Destroy() {}

// setContextUsage updates the ring for the open conversation.
func (u *desktopUI) setContextUsage(used, window int) {
	u.contextRing.set(used, window)
}

// refreshContextRing recomputes the usage of the open conversation from its
// history, so the ring is right after opening a chat or when nothing runs.
func (u *desktopUI) refreshContextRing() {
	if u.current == nil {
		u.setContextUsage(0, modelContextWindow(u.cfg))
		return
	}
	usage := 0
	for _, m := range u.current.History {
		if m.Usage > usage {
			usage = m.Usage
		}
	}
	u.setContextUsage(usage, modelContextWindow(u.current.Config))
}
