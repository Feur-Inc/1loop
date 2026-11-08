// SPDX-License-Identifier: MPL-2.0

package ui

// The browser client's attachments (issue #267): the page's own file
// chooser, pasted pictures and dropped files. A page has no paths to give,
// only bytes, which go to the server as the desktop's server chats send
// theirs (remoteDesktop.attach).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall/js"

	"fyne.io/fyne/v2"

	"1loop/internal/remoteapi"
)

// attachInputID names the hidden <input type=file>, for the smoke run.
const attachInputID = "loop-attach"

// pickAttachments opens the browser's file chooser. Browsers open it only
// shortly after a click or a key: the menu's click usually still counts;
// when it no longer does, the next click on the page opens it.
func (remote *remoteDesktop) pickAttachments(images bool) {
	input := attachInput(remote.parent)
	input.Set("accept", "")
	if images {
		input.Set("accept", "image/*")
	}
	activation := js.Global().Get("navigator").Get("userActivation")
	if activation.IsUndefined() || activation.Get("isActive").Bool() {
		input.Call("click")
		return
	}
	remote.status.SetText("Click to choose the files")
	var open js.Func
	open = js.FuncOf(func(js.Value, []js.Value) any {
		input.Call("click")
		open.Release()
		return nil
	})
	js.Global().Get("document").Call("addEventListener", "pointerup", open, map[string]any{"once": true})
}

var attachInputElement js.Value

func attachInput(ui *desktopUI) js.Value {
	if !attachInputElement.IsUndefined() {
		return attachInputElement
	}
	document := js.Global().Get("document")
	input := document.Call("createElement", "input")
	input.Set("type", "file")
	input.Set("multiple", true)
	input.Set("id", attachInputID)
	input.Get("style").Set("display", "none")
	input.Call("addEventListener", "change", js.FuncOf(func(js.Value, []js.Value) any {
		files := fileList(input.Get("files"))
		input.Set("value", "") // the same file can be chosen again
		ui.attachBrowserFiles(files, "")
		return nil
	}))
	document.Get("body").Call("appendChild", input)
	attachInputElement = input
	return input
}

// listenBrowserFiles attaches what is pasted or dropped on the page.
func (ui *desktopUI) listenBrowserFiles() {
	window, document := js.Global(), js.Global().Get("document")
	attachInput(ui)
	// The canvas takes every key and stops the browser's own handling, the
	// paste with it: Ctrl+V skips it here, before it gets there, and what
	// is pasted comes in the paste event, pictures and files included.
	pasting := false
	window.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		event := args[0]
		if strings.EqualFold(event.Get("key").String(), "v") && (event.Get("ctrlKey").Bool() || event.Get("metaKey").Bool()) && !event.Get("altKey").Bool() {
			pasting = true
			event.Call("stopImmediatePropagation")
		}
		return nil
	}), true)
	window.Call("addEventListener", "keyup", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if pasting && strings.EqualFold(args[0].Get("key").String(), "v") {
			pasting = false
			args[0].Call("stopImmediatePropagation")
		}
		return nil
	}), true)
	document.Call("addEventListener", "paste", js.FuncOf(func(_ js.Value, args []js.Value) any {
		event := args[0]
		data := event.Get("clipboardData")
		if data.IsNull() || data.IsUndefined() {
			return nil
		}
		event.Call("preventDefault")
		var files []js.Value
		items := data.Get("items")
		for i := 0; i < items.Get("length").Int(); i++ {
			if item := items.Index(i); item.Get("kind").String() == "file" {
				if file := item.Call("getAsFile"); !file.IsNull() {
					files = append(files, file)
				}
			}
		}
		if len(files) > 0 {
			ui.attachBrowserFiles(files, "pasted-image")
			return nil
		}
		text := data.Call("getData", "text/plain").String()
		go fyne.Do(func() { ui.pasteText(text) })
		return nil
	}))
	document.Call("addEventListener", "dragover", js.FuncOf(func(_ js.Value, args []js.Value) any {
		event := args[0]
		event.Call("preventDefault")
		if transfer := event.Get("dataTransfer"); !transfer.IsNull() {
			transfer.Set("dropEffect", "copy")
		}
		return nil
	}))
	document.Call("addEventListener", "drop", js.FuncOf(func(_ js.Value, args []js.Value) any {
		event := args[0]
		event.Call("preventDefault")
		if transfer := event.Get("dataTransfer"); !transfer.IsNull() {
			ui.attachBrowserFiles(fileList(transfer.Get("files")), "")
		}
		return nil
	}))
}

// pasteText gives pasted text to the focused widget, as the canvas's own
// Ctrl+V did.
func (ui *desktopUI) pasteText(text string) {
	if target, ok := ui.window.Canvas().Focused().(fyne.Shortcutable); ok {
		target.TypedShortcut(&fyne.ShortcutPaste{Clipboard: pastedText(text)})
	}
}

// pastedText is a clipboard holding what one paste brought.
type pastedText string

func (text pastedText) Content() string { return string(text) }
func (pastedText) SetContent(string)    {}

// pasteBrowserImage is the attach menu's Paste image: the browser asks
// before letting the page read the clipboard, and Ctrl+V needs no asking.
func (ui *desktopUI) pasteBrowserImage() bool {
	clipboard := js.Global().Get("navigator").Get("clipboard")
	if clipboard.IsUndefined() || clipboard.Get("read").IsUndefined() {
		ui.notice("Paste", "Press Ctrl+V to paste an image.")
		return true
	}
	read := clipboard.Call("read")
	go func() {
		items, err := await(read)
		var files []js.Value
		for i := 0; err == nil && i < items.Get("length").Int(); i++ {
			item := items.Index(i)
			types := item.Get("types")
			for t := 0; t < types.Get("length").Int(); t++ {
				if kind := types.Index(t).String(); strings.HasPrefix(kind, "image/") {
					blob, blobErr := await(item.Call("getType", kind))
					if blobErr == nil {
						files = append(files, blob)
					}
					break
				}
			}
		}
		fyne.Do(func() {
			switch {
			case err != nil:
				ui.notice("Paste", "Could not read the clipboard: "+err.Error()+". Press Ctrl+V to paste an image.")
			case len(files) == 0:
				ui.notice("Paste", errNoClipboardImage.Error()+".")
			default:
				ui.attachBrowserFiles(files, "pasted-image")
			}
		})
	}()
	return true
}

// attachBrowserFiles uploads Files (or Blobs) to the server chat on
// screen. unnamed names those without a name, with their type's extension.
func (ui *desktopUI) attachBrowserFiles(files []js.Value, unnamed string) {
	if len(files) == 0 {
		return
	}
	var uploads []remoteUpload
	var problems []string
	for _, file := range files {
		name := ""
		if value := file.Get("name"); value.Type() == js.TypeString {
			name = value.String()
		}
		if name == "" || unnamed != "" && name == "image.png" { // Chrome's name for any pasted picture
			name = unnamed
			if _, kind, ok := strings.Cut(file.Get("type").String(), "/"); ok {
				name += "." + strings.TrimSuffix(kind, "+xml")
			}
		}
		if size := file.Get("size").Float(); size > remoteapi.AttachmentLimit {
			problems = append(problems, fmt.Sprintf("%s: larger than %d MB", name, remoteapi.AttachmentLimit>>20))
			continue
		}
		uploads = append(uploads, remoteUpload{name: name, open: func() (io.ReadCloser, error) {
			buffer, err := await(file.Call("arrayBuffer"))
			if err != nil {
				return nil, err
			}
			array := js.Global().Get("Uint8Array").New(buffer)
			data := make([]byte, array.Get("length").Int())
			js.CopyBytesToGo(data, array)
			return io.NopCloser(bytes.NewReader(data)), nil
		}})
	}
	go fyne.Do(func() {
		if len(problems) > 0 {
			ui.notice("Attach files", strings.Join(problems, "\n"))
		}
		if ui.server == nil {
			ui.notice("Attach files", "Connect to the server first.")
			return
		}
		if len(uploads) > 0 {
			ui.server.attach(uploads)
		}
	})
}

// fileList is a FileList's files.
func fileList(list js.Value) []js.Value {
	if list.IsNull() || list.IsUndefined() {
		return nil
	}
	files := make([]js.Value, list.Get("length").Int())
	for i := range files {
		files[i] = list.Index(i)
	}
	return files
}

// await waits for a promise; never from a callback, which the page runs
// one at a time.
func await(promise js.Value) (js.Value, error) {
	done := make(chan struct{})
	var value js.Value
	var err error
	resolve := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			value = args[0]
		}
		close(done)
		return nil
	})
	reject := js.FuncOf(func(_ js.Value, args []js.Value) any {
		err = errors.New("failed")
		if len(args) > 0 {
			err = errors.New(args[0].Call("toString").String())
		}
		close(done)
		return nil
	})
	defer resolve.Release()
	defer reject.Release()
	promise.Call("then", resolve, reject)
	<-done
	return value, err
}
