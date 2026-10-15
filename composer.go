//go:build !server

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
	measure  *widget.RichText
	resized  func()
	fitting  bool
}

const promptMaxRows = 10

func newChatEntry() *chatEntry {
	entry := &chatEntry{rows: 1, measure: widget.NewRichTextWithText("")}
	entry.measure.Wrapping = fyne.TextWrapWord
	entry.MultiLine = true
	entry.Wrapping = fyne.TextWrapWord
	entry.ExtendBaseWidget(entry)
	entry.SetMinRowsVisible(1)
	entry.OnChanged = func(string) { entry.fit() }
	return entry
}

// fit sizes the entry to its wrapped text so the composer grows as the
// prompt does, then scrolls past promptMaxRows.
func (entry *chatEntry) fit() {
	if entry.fitting || entry.Size().Width <= 0 {
		return
	}
	entry.fitting = true
	defer func() { entry.fitting = false }()
	th := entry.Theme()
	width := entry.Size().Width - 2*th.Size(theme.SizeNameInputBorder)
	segment := entry.measure.Segments[0].(*widget.TextSegment)
	segment.Text = entry.Text
	segment.Style.TextStyle = entry.TextStyle
	entry.measure.Refresh()
	entry.measure.Resize(fyne.NewSize(width, 1))
	line := textSize("M", th.Size(theme.SizeNameText), entry.TextStyle).Height
	rows := max(1, min(promptMaxRows, int(math.Round(float64((entry.measure.MinSize().Height-2*th.Size(theme.SizeNameInnerPadding))/line)))))
	if rows != entry.rows {
		entry.rows = rows
		entry.SetMinRowsVisible(rows)
		if entry.resized != nil {
			entry.resized()
		}
	}
}

func (entry *chatEntry) Resize(size fyne.Size) {
	entry.Entry.Resize(size)
	entry.fit()
}

// The composer card shows focus itself, so the entry's own outline is hidden.
// Its width cannot be themed to zero: Fyne sizes the text cursor with it.
func (entry *chatEntry) CreateRenderer() fyne.WidgetRenderer {
	return &borderlessRenderer{WidgetRenderer: entry.Entry.CreateRenderer()}
}

type borderlessRenderer struct{ fyne.WidgetRenderer }

func (renderer *borderlessRenderer) hide() {
	for _, object := range renderer.WidgetRenderer.Objects() {
		if rect, ok := object.(*canvas.Rectangle); ok && rect.StrokeWidth > 0 {
			rect.StrokeColor = color.Transparent
		}
	}
}

func (renderer *borderlessRenderer) Refresh() { renderer.WidgetRenderer.Refresh(); renderer.hide() }
func (renderer *borderlessRenderer) Layout(size fyne.Size) {
	renderer.WidgetRenderer.Layout(size)
	renderer.hide()
}

// App shortcuts must work while typing: Fyne hands every shortcut to the
// focused entry, which ignores the ones it does not know. Ctrl+Enter also
// arrives here, as a shortcut rather than a key.
func (entry *chatEntry) TypedShortcut(s fyne.Shortcut) {
	if c, ok := s.(*desktop.CustomShortcut); ok && c.Modifier == fyne.KeyModifierControl &&
		(c.KeyName == fyne.KeyReturn || c.KeyName == fyne.KeyEnter) {
		entry.steer()
		return
	}
	if _, ok := s.(*fyne.ShortcutPaste); ok && entry.paste != nil && entry.paste() {
		return // an image was staged as an attachment
	}
	if entry.shortcut != nil && entry.shortcut(s) {
		return
	}
	entry.Entry.TypedShortcut(s)
}

func (entry *chatEntry) TypedKey(key *fyne.KeyEvent) {
	if key.Name == fyne.KeyEscape && entry.escape != nil {
		entry.escape()
		return
	}
	if key.Name == fyne.KeyReturn || key.Name == fyne.KeyEnter {
		shift := false
		if driver, ok := fyne.CurrentApp().Driver().(desktop.Driver); ok {
			shift = driver.CurrentKeyModifiers()&fyne.KeyModifierShift != 0
		}
		if !shift {
			entry.send()
			return
		}
	}
	entry.Entry.TypedKey(key) // Shift+Enter: a new line
}

func (entry *chatEntry) FocusGained() {
	entry.Entry.FocusGained()
	if entry.onFocus != nil {
		entry.onFocus(true)
	}
}

func (entry *chatEntry) FocusLost() {
	entry.Entry.FocusLost()
	if entry.onFocus != nil {
		entry.onFocus(false)
	}
}

// On narrow conversations, the two groups need separate lines when the
// side panel is open. The split supplies the available width before sizing.
type composerControlsLayout struct{ available float32 }

func (layoutSpec *composerControlsLayout) MinSize(o []fyne.CanvasObject) fyne.Size {
	size, sizeValue := o[0].MinSize(), o[1].MinSize()
	if layoutSpec.available > 0 && size.Width+sizeValue.Width+8 > layoutSpec.available {
		return fyne.NewSize(fyne.Max(size.Width, sizeValue.Width), size.Height+sizeValue.Height+4)
	}
	return fyne.NewSize(size.Width+sizeValue.Width+8, fyne.Max(size.Height, sizeValue.Height))
}

func (layoutSpec *composerControlsLayout) Layout(o []fyne.CanvasObject, size fyne.Size) {
	sizeValue, b := o[0].MinSize(), o[1].MinSize()
	y := (size.Height - b.Height) / 2
	if sizeValue.Width+b.Width+8 > size.Width {
		o[0].Move(fyne.NewPos(0, 0))
		y = sizeValue.Height + 4
	} else {
		o[0].Move(fyne.NewPos(0, (size.Height-sizeValue.Height)/2))
	}
	o[0].Resize(sizeValue)
	o[1].Move(fyne.NewPos(fyne.Max(0, size.Width-b.Width), y))
	o[1].Resize(b)
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
	line := textSize("Ag", theme.TextSize(), fyne.TextStyle{}).Height
	btn.Move(fyne.NewPos(0, fyne.Max(0, theme.InnerPadding()+line/2-btnSize.Height/2)))
	reserved := btnSize.Width + attachButtonGap
	input.Resize(fyne.NewSize(size.Width-reserved, size.Height))
	input.Move(fyne.NewPos(reserved, 0))
}

// ---------------------------------------------------------------------------
// Image attachments: pasted from the clipboard or picked from files, shown
// as thumbnails above the prompt, and sent to the model as data URLs.

var imageMime = regexp.MustCompile(`image/[a-z0-9.+-]+`)

func linuxImageClipboardData() ([]byte, error) {
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
func (ui *desktopUI) pasteFromClipboard() bool {
	raw, err := imageClipboardData()
	if err != nil {
		return false
	}
	img, err := decodeImage(raw)
	if err != nil {
		ui.notice("Paste", err.Error())
		return true
	}
	ui.attachImage(img)
	return true
}

// pickImages attaches pictures chosen in the desktop's file chooser.
func (ui *desktopUI) pickImages() {
	ui.pick(pickRequest{title: "Attach images", start: ui.cfg.Workspace, multiple: true, filterName: "Images",
		extensions: []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp"}}, func(paths []string) {
		for _, path := range paths {
			file, err := os.Open(path)
			if err != nil {
				ui.notice("Image", err.Error())
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(file, 25<<20))
			file.Close()
			if err != nil {
				ui.notice("Image", err.Error())
				continue
			}
			img, err := decodeImage(raw)
			if err != nil {
				ui.notice("Image", path+": "+err.Error())
				continue
			}
			ui.attachImage(img)
		}
	})
}

// attachImage keeps a picture, as PNG, until the next message.
func (ui *desktopUI) attachImage(img *image.NRGBA) {
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
		ui.notice("Image", err.Error())
		return
	}
	ui.attachments = append(ui.attachments, ImagePart{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(buf.Bytes())})
	ui.renderAttachments()
}

func (ui *desktopUI) renderAttachments() {
	ui.attachRow.RemoveAll()
	for i, file := range ui.fileAttachments {
		i := i
		remove := newRow("", iconClose, styleWindow, func() {
			ui.fileAttachments = append(ui.fileAttachments[:i], ui.fileAttachments[i+1:]...)
			ui.renderAttachments()
		})
		label := textLine(fitText(file.Name, 12.5, fyne.TextStyle{}, 180), 12.5, colText)
		ui.attachRow.Add(surface(container.NewHBox(label, remove), colCode, colBorderSoft, 8, 6))
	}
	for i, part := range ui.attachments {
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
			ui.attachments = append(ui.attachments[:i], ui.attachments[i+1:]...)
			ui.renderAttachments()
		})
		ui.attachRow.Add(container.NewStack(surface(view, colCode, colBorderSoft, 8, 0), container.NewBorder(nil, nil, nil, remove)))
	}
	if len(ui.attachRow.Objects) == 0 {
		ui.attachTray.Hide()
	} else {
		ui.attachTray.Show()
	}
}

// takeAttachments hands the staged pictures to a message and clears them.
func (ui *desktopUI) takeAttachments() []ImagePart {
	images := ui.attachments
	ui.attachments = nil
	if len(images) > 0 {
		ui.renderAttachments()
	}
	return images
}

// ---------------------------------------------------------------------------
// While a task runs, Enter queues the message: it becomes the next turn once
// the model has answered. Ctrl+Enter steers instead: the message joins the
// running task at the next gap between two model calls (after the current
// tool calls). Both wait, per chat, in a tray above the composer, where they
// can be reordered, edited in the prompt, sent at once or dropped.

func (ui *desktopUI) takeSteer(chat *conversation) []message {
	out := chat.steer
	chat.steer = nil
	if len(out) > 0 {
		ui.remote.metaChanged(chat)
	}
	if len(out) > 0 && chat == ui.current {
		ui.renderQueue()
	}
	return out
}

func (ui *desktopUI) takeNext(chat *conversation) (message, bool) {
	if len(chat.queue) == 0 {
		return message{}, false
	}
	next := chat.queue[0]
	chat.queue = chat.queue[1:]
	ui.remote.metaChanged(chat)
	if chat == ui.current {
		ui.renderQueue()
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

func (ui *desktopUI) moveQueued(index, delta int) {
	queue := ui.current.queue
	to := index + delta
	if index < 0 || to < 0 || to >= len(queue) {
		return
	}
	queue[index], queue[to] = queue[to], queue[index]
	ui.renderQueue()
}

func (ui *desktopUI) dropQueued(steer bool, index int) {
	waiting := lane(ui.current, steer)
	if index < 0 || index >= len(*waiting) {
		return
	}
	*waiting = append((*waiting)[:index], (*waiting)[index+1:]...)
	ui.renderQueue()
}

// sendQueuedNow steers a queued message into the running task, or sends it
// right away when nothing runs.
func (ui *desktopUI) sendQueuedNow(index int) {
	chat := ui.current
	if index < 0 || index >= len(chat.queue) {
		return
	}
	message := chat.queue[index]
	chat.queue = append(chat.queue[:index], chat.queue[index+1:]...)
	if chat.run != nil {
		chat.steer = append(chat.steer, message)
		ui.renderQueue()
		return
	}
	ui.renderQueue()
	ui.start(message)
}

func (ui *desktopUI) clearQueue() {
	if ui.current != nil {
		ui.current.queue, ui.current.steer = nil, nil
	}
	ui.renderQueue()
}

// renderQueue shows the open chat's waiting messages.
func (ui *desktopUI) renderQueue() {
	ui.queueList.RemoveAll()
	chat := ui.current
	ui.remote.metaChanged(chat)
	if chat == nil || len(chat.steer)+len(chat.queue) == 0 {
		ui.queueTray.Hide()
		return
	}
	for i, message := range chat.steer {
		ui.queueList.Add(ui.queueRow(true, i, message))
	}
	for i, messageValue := range chat.queue {
		ui.queueList.Add(ui.queueRow(false, i, messageValue))
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
	ui.queueTitle.SetText(label)
	ui.queueTray.Show()
}

// queueRow shows one waiting message; its actions appear on hover.
func (ui *desktopUI) queueRow(steer bool, index int, messageValue message) fyne.CanvasObject {
	text := strings.Join(strings.Fields(messageValue.Content), " ")
	if text == "" {
		text = "[image]"
	} else if len(messageValue.Images) > 0 {
		text += fmt.Sprintf("  [+%d image]", len(messageValue.Images))
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
	label := newRow(text, nil, styleChip, func() { ui.editQueued(steer, index) })
	label.size = 13.5
	label.truncate = true
	tips := map[*tapRow]string{}
	var actions []*tapRow
	action := func(icon fyne.Resource, tip string, run func()) {
		tapRowValue := newRow("", icon, styleWindow, run)
		tapRowValue.alpha = 0
		tips[tapRowValue] = tip
		actions = append(actions, tapRowValue)
	}
	if !steer {
		if index > 0 {
			action(iconChevronUp, "Move up", func() { ui.moveQueued(index, -1) })
		}
		if index < len(ui.current.queue)-1 {
			action(iconChevron, "Move down", func() { ui.moveQueued(index, +1) })
		}
		action(iconArrowTurn, "Send now, at the next step", func() { ui.sendQueuedNow(index) })
	}
	action(iconRename, "Edit in the prompt", func() { ui.editQueued(steer, index) })
	action(iconClose, "Remove", func() { ui.dropQueued(steer, index) })
	bar := container.New(layout.NewCustomPaddedHBoxLayout(0))
	for _, a := range actions {
		bar.Add(a)
	}
	hover := canvas.NewRectangle(color.Transparent)
	hover.CornerRadius = 8
	row := container.NewStack(hover, padded(0, 0, 4, 2, container.NewBorder(nil, nil, padded(0, 0, 0, 4, badge), bar, label)))
	revealOnHover(label, hover, actions, tips, ui.tips)
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
			for i, row := range actions {
				fadeRow(row, target, &anims[i])
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
	for _, tapRowValue := range actions {
		a := tapRowValue
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
	files    []fileAttachment
}

func (ui *desktopUI) editQueued(steer bool, index int) {
	ui.finishEdit(false)
	chat := ui.current
	waiting := lane(chat, steer)
	if index < 0 || index >= len(*waiting) {
		return
	}
	message := (*waiting)[index]
	*waiting = append((*waiting)[:index], (*waiting)[index+1:]...)
	ui.editing = &queueEdit{chat: chat, steer: steer, index: index, original: message, draft: ui.prompt.Text, images: ui.attachments, files: ui.fileAttachments}
	ui.fileAttachments = nil
	ui.prompt.SetText(message.Content)
	lines := strings.Split(message.Content, "\n")
	ui.prompt.CursorRow, ui.prompt.CursorColumn = len(lines)-1, len([]rune(lines[len(lines)-1]))
	ui.prompt.Refresh()
	ui.attachments = append([]ImagePart(nil), message.Images...)
	ui.renderAttachments()
	ui.editBar.Show()
	ui.renderQueue()
	ui.window.Canvas().Focus(ui.prompt)
}

// finishEdit puts the message back in its place: as edited when commit is
// set (dropped if emptied), unchanged otherwise; the prompt gets its draft
// back.
func (ui *desktopUI) finishEdit(commit bool) {
	if commit {
		if problem := ui.attachmentProblem(); problem != "" {
			ui.notice("Attachments", problem)
			return
		}
	}
	queueEdit := ui.editing
	if queueEdit == nil {
		return
	}
	ui.editing = nil
	messageValue := queueEdit.original
	if commit {
		messageValue.Content, messageValue.Images = attachmentText(strings.TrimSpace(ui.prompt.Text), ui.fileAttachments), ui.attachments
	}
	if messageValue.Content != "" || len(messageValue.Images) > 0 {
		waiting := lane(queueEdit.chat, queueEdit.steer)
		at := min(queueEdit.index, len(*waiting))
		*waiting = append((*waiting)[:at], append([]message{messageValue}, (*waiting)[at:]...)...)
	}
	ui.prompt.SetText(queueEdit.draft)
	ui.attachments = queueEdit.images
	ui.fileAttachments = queueEdit.files
	ui.renderAttachments()
	ui.editBar.Hide()
	ui.renderQueue()
}

// ---------------------------------------------------------------------------
// contextRing shows how much of the model's window the conversation fills:
// amber for what is used, white for what is left. Hovering gives the figures.

type contextRing struct {
	widget.BaseWidget
	used, window int
	plan         planUsage // Claude Code: how much of the subscription's windows is used
	tips         *tipLayer
	track, fill  *canvas.Arc
}

func newContextRing(tips *tipLayer) *contextRing {
	ring := &contextRing{tips: tips}
	ring.track = canvas.NewArc(0, 360, 0.76, colText)
	ring.fill = canvas.NewArc(0, 0, 0.76, colAmber)
	ring.ExtendBaseWidget(ring)
	return ring
}

func (ring *contextRing) set(used, window int) {
	ring.used, ring.window = used, window
	frac := float32(0)
	if window > 0 {
		frac = fyne.Min(1, float32(used)/float32(window))
	}
	ring.fill.EndAngle = 360 * frac
	ring.fill.Refresh()
	if ring.tips.anchor == ring {
		ring.tips.place(ring, ring.describe())
	}
}

func (ring *contextRing) describe() string {
	if ring.window <= 0 {
		return "Context usage\nAvailable once a model is selected"
	}
	percent := ring.used * 100 / ring.window
	if percent > 100 {
		percent = 100
	}
	text := fmt.Sprintf("%d%% of the context used\n%s of %s tokens", percent, tokens(ring.used), tokens(ring.window))
	if ring.plan != (planUsage{}) {
		text += fmt.Sprintf("\nClaude plan: %.0f%% of the 5 h window · %.0f%% of the week", ring.plan.FiveHour*100, ring.plan.Week*100)
	}
	return text
}

func (ring *contextRing) MouseIn(*desktop.MouseEvent)    { ring.tips.show(ring, ring.describe()) }
func (ring *contextRing) MouseMoved(*desktop.MouseEvent) {}
func (ring *contextRing) MouseOut()                      { ring.tips.hide() }

func (ring *contextRing) CreateRenderer() fyne.WidgetRenderer { return &contextRingRenderer{ring} }

type contextRingRenderer struct{ ring *contextRing }

const contextRingSize = 16

func (renderer *contextRingRenderer) MinSize() fyne.Size {
	return fyne.NewSquareSize(contextRingSize + 12)
}

func (renderer *contextRingRenderer) Layout(size fyne.Size) {
	at := fyne.NewPos((size.Width-contextRingSize)/2, (size.Height-contextRingSize)/2)
	for _, arc := range []*canvas.Arc{renderer.ring.track, renderer.ring.fill} {
		arc.Move(at)
		arc.Resize(fyne.NewSquareSize(contextRingSize))
	}
}

func (renderer *contextRingRenderer) Refresh() {
	renderer.ring.track.Refresh()
	renderer.ring.fill.Refresh()
}

func (renderer *contextRingRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{renderer.ring.track, renderer.ring.fill}
}

func (renderer *contextRingRenderer) Destroy() {}

// setContextUsage updates the ring for the open conversation.
func (ui *desktopUI) setContextUsage(used, window int) {
	ui.contextRing.set(used, window)
	if ui.panel != nil {
		ui.panel.refreshTodoStatus()
	}
}

// refreshContextRing recomputes the usage of the open conversation from its
// history, so the ring is right after opening a chat or when nothing runs.
func (ui *desktopUI) refreshContextRing() {
	ui.contextRing.plan = planUsage{}
	if ui.cfg.Provider == "claudecode" {
		ui.contextRing.plan = ui.plan
	}
	if ui.current == nil {
		ui.setContextUsage(0, modelContextWindow(ui.cfg))
		return
	}
	usage := 0
	for _, message := range ui.current.History {
		if message.Usage > usage {
			usage = message.Usage
		}
	}
	ui.setContextUsage(usage, modelContextWindow(ui.current.Config))
}
