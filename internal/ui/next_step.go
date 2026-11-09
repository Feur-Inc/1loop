// SPDX-License-Identifier: MPL-2.0

package ui

// The next-step card (#215): the model's wrap_up, drawn under the turn's
// last message with the question it leaves the user and one or two replies.
// The live card sits after the transcript (ui.nextStep); once the user has
// written, the card stays above their message, answered, with no buttons.

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
	"1loop/internal/core"
)

// nextStepCard draws wrap. reply sends a button's message; nil draws the
// card answered with chosen, the reply the user sent.
func nextStepCard(wrap agent.WrapUp, reply func(string), chosen string) fyne.CanvasObject {
	text := func(value string, size float32, ink color.Color, bold bool) fyne.CanvasObject {
		flow := newFlowText(plainSpans(value), size, ink)
		flow.tail, flow.tight, flow.bold = "", true, bold
		return flow
	}
	lines := []fyne.CanvasObject{text(wrap.Recap, 13.5, colMuted, false), text(wrap.Ask, 14.5, colText, true)}
	if reply != nil {
		buttons := []fyne.CanvasObject{layout.NewSpacer()}
		for i := len(wrap.Actions) - 1; i >= 0; i-- { // the model's pick, the main button, last
			action := wrap.Actions[i]
			style := styleOutline
			if i == 0 {
				style = stylePrimary
			}
			buttons = append(buttons, newRow(action.Label, nil, style, func() { reply(action.Reply()) }))
		}
		lines = append(lines, padded(4, 0, 0, 0, container.New(layout.NewCustomPaddedHBoxLayout(8), buttons...)))
	} else if label := chosenLabel(wrap, chosen); label != "" {
		mark := canvas.NewImageFromResource(tinted(iconCheck, colFaint))
		mark.SetMinSize(fyne.NewSquareSize(14))
		lines = append(lines, container.NewBorder(nil, nil, container.NewCenter(mark), nil, padded(0, 0, 6, 0, textLine(label, 12.5, colFaint))))
	}
	return surface(container.New(layout.NewCustomPaddedVBoxLayout(8), lines...), colSurface, colBorder, 12, 14)
}

// chosenLabel is the button whose reply the user sent, "" for their own words.
func chosenLabel(wrap agent.WrapUp, sent string) string {
	sent = strings.TrimSpace(sent)
	for _, action := range wrap.Actions {
		if sent == strings.TrimSpace(action.Reply()) {
			return action.Label
		}
	}
	return ""
}

// answeredWrapUp is the card the turn before the user's message at index
// ended on, if it asked something.
func answeredWrapUp(items []agent.ChatItem, index int) (agent.WrapUp, bool) {
	for i := index - 1; i >= 0 && items[i].Kind != "user"; i-- {
		if items[i].Kind == "tool" && agent.WrapUpTool(items[i].Tool) && items[i].Failed == "" {
			wrap, err := agent.ParseWrapUp(items[i].Args)
			return wrap, err == nil && wrap.Card()
		}
	}
	return agent.WrapUp{}, false
}

// liveWrapUp is the current turn's card, while the user has not answered it.
func liveWrapUp(items []agent.ChatItem) (agent.WrapUp, int) {
	for i := len(items) - 1; i >= 0 && items[i].Kind != "user"; i-- {
		item := items[i]
		if item.Kind == "tool" && agent.WrapUpTool(item.Tool) && item.Failed == "" && item.Text != "" { // Text: the call has run
			if wrap, err := agent.ParseWrapUp(item.Args); err == nil && wrap.Card() {
				return wrap, i
			}
			return agent.WrapUp{}, -1
		}
	}
	return agent.WrapUp{}, -1
}

// syncNextStep shows the open chat's live card after the transcript. It is
// rebuilt only when the call it shows changes.
func (ui *desktopUI) syncNextStep() {
	if ui.nextStep == nil {
		return
	}
	key := ""
	var wrap agent.WrapUp
	chat := ui.current
	if chat != nil && ui.loadingChat == nil {
		var index int
		if wrap, index = liveWrapUp(chat.Items); index >= 0 {
			key = chat.ID + "/" + chat.Items[index].ID
		}
	}
	if key == ui.nextStepKey {
		return
	}
	ui.nextStepKey = key
	ui.nextStep.RemoveAll()
	if key != "" {
		ui.nextStep.Add(padded(transcriptGap, 0, 0, 0, nextStepCard(wrap, func(text string) { ui.sendReply(chat, text) }, "")))
	}
	ui.nextStep.Refresh()
}

// sendReply sends a card's reply as the user's message, as if typed; the
// draft in the composer stays.
func (ui *desktopUI) sendReply(chat *conversation, text string) {
	if chat != ui.current {
		return
	}
	if ui.server != nil {
		ui.server.post(ui.server.current, text, "send", nil, nil, "sending:"+core.Identifier()+"|"+ui.server.draftKey(ui.server.current))
		return
	}
	message := agent.PromptMessage(text, nil, nil)
	if chat.run != nil { // the closing message is still coming: the reply joins at its next step
		chat.steer = append(chat.steer, message)
		ui.renderQueue()
		ui.followTail = true
		ui.relayoutColumn()
		return
	}
	ui.start(message)
}

// iconWaiting marks a chat in the sidebar whose wrap-up waits on the user:
// a ring, where a running chat has a dot.
var iconWaiting = lineIcon("waiting", `<circle cx="8" cy="8" r="3.4"/>`)
