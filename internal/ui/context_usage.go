package ui

// A chat's context window by category, under its task list (#106), as
// Claude Code's /context shows it: the used share as a bar and a
// percentage, a row per category with its tokens and share of the window,
// the deferred tools apart, and the MCP tools in a list that opens.

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"1loop/internal/agent"
)

// contextUsage is a chat's breakdown (the new chat's when chat is nil).
func (ui *desktopUI) contextUsage(chat *conversation) *agent.ContextUsage {
	if chat == nil {
		return agent.ChatContextUsage(ui.resolveModel(ui.cfg), nil, nil, ui.classicCompaction)
	}
	return agent.ChatContextUsage(ui.resolveModel(chat.Config), chat.History, chat.ContextUsage, ui.classicCompaction)
}

// contextUsageState is what the user opened, kept across redraws.
type contextUsageState struct {
	closed    bool // the rows put away under the header
	toolsOpen bool
	tools     []agent.ContextTool // the MCP tools' list, when it comes apart from the usage
}

// contextColors are the categories' colors; the others are greys.
var contextColors = map[string]color.Color{
	"messages":           color.NRGBA{R: 0x4c, G: 0x8d, B: 0xff, A: 0xff},
	"system_tools":       color.NRGBA{R: 0xe8, G: 0x81, B: 0x3a, A: 0xff},
	"mcp_tools":          color.NRGBA{R: 0x2f, G: 0xb5, B: 0x9b, A: 0xff},
	"autocompact_buffer": color.NRGBA{R: 0x4a, G: 0x4a, B: 0x4a, A: 0xff},
	"compact_buffer":     color.NRGBA{R: 0x4a, G: 0x4a, B: 0x4a, A: 0xff},
	"free_space":         color.NRGBA{R: 0x2a, G: 0x2a, B: 0x2a, A: 0xff},
}

func contextColor(category agent.ContextCategory) color.Color {
	if c, ok := contextColors[category.ID]; ok {
		return c
	}
	if category.Kind == "deferred" {
		return color.NRGBA{R: 0x3a, G: 0x3a, B: 0x3a, A: 0xff}
	}
	return color.NRGBA{R: 0x9a, G: 0x9a, B: 0x9a, A: 0xff}
}

// contextBarLayout gives each segment its share of the width.
type contextBarLayout struct{ shares []float32 }

func (bar contextBarLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(40, 4) }
func (bar contextBarLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(size) // the track
	x := float32(0)
	for i, share := range bar.shares {
		width := size.Width * share
		objects[i+1].Move(fyne.NewPos(x, 0))
		objects[i+1].Resize(fyne.NewSize(width, size.Height))
		x += width
	}
}

func contextShare(tokens, window int) string {
	return fmt.Sprintf("%.1f%%", float64(tokens)*100/float64(window))
}

// contextUsageView draws the usage; tools asks for the MCP tools' list when
// the usage leaves it out (a remote chat), and changed redraws the view.
func contextUsageView(usage *agent.ContextUsage, state *contextUsageState, tools func(), changed func()) fyne.CanvasObject {
	window := max(1, usage.Window)
	chevron := iconChevron
	if state.closed {
		chevron = iconChevronRight
	}
	header := newRow("Context", chevron, styleChip, func() { state.closed = !state.closed; changed() })
	header.iconAfter = true
	total := container.NewBorder(nil, nil, textLine(fmt.Sprintf("%d%% used", min(100, usage.Used*100/window)), 13, colText), nil,
		container.NewHBox(layout.NewSpacer(), textLine(agent.Tokens(usage.Used)+" / "+agent.Tokens(usage.Window), 12, colMuted)))
	track := canvas.NewRectangle(colBorder)
	track.CornerRadius = 2
	segments := []fyne.CanvasObject{track}
	var shares []float32
	for _, category := range usage.Categories {
		if category.Kind == "used" && category.Tokens > 0 {
			segment := canvas.NewRectangle(contextColor(category))
			segments, shares = append(segments, segment), append(shares, float32(category.Tokens)/float32(window))
		}
	}
	bar := container.New(contextBarLayout{shares: shares}, segments...)
	box := container.New(layout.NewCustomPaddedVBoxLayout(6), container.NewHBox(header), total, bar)
	if state.closed {
		return box
	}
	row := func(category agent.ContextCategory) fyne.CanvasObject {
		swatch := canvas.NewRectangle(contextColor(category))
		swatch.CornerRadius = 2
		swatch.SetMinSize(fyne.NewSquareSize(9))
		share := "—" // deferred tools are outside the window
		if category.Kind != "deferred" {
			share = contextShare(category.Tokens, window)
		}
		name := textLine(category.Name, 12, colText)
		if category.Kind == "deferred" {
			name.Color = colMuted
		}
		return container.NewBorder(nil, nil, container.NewHBox(container.NewCenter(swatch), name), nil,
			container.NewHBox(layout.NewSpacer(), textLine(agent.Tokens(category.Tokens), 12, colMuted), sized(46, container.NewHBox(layout.NewSpacer(), textLine(share, 12, colMuted)))))
	}
	rows := container.New(layout.NewCustomPaddedVBoxLayout(4))
	deferred := container.New(layout.NewCustomPaddedVBoxLayout(4))
	for _, category := range usage.Categories {
		switch {
		case category.Kind == "deferred":
			if category.Tokens > 0 {
				deferred.Add(row(category))
			}
		case category.Tokens > 0 || category.Kind != "used":
			rows.Add(row(category))
		}
	}
	box.Add(padded(4, 0, 0, 0, rows))
	if len(deferred.Objects) > 0 {
		box.Add(padded(6, 0, 0, 0, deferred))
	}
	list := usage.MCPTools
	if list == nil {
		list = state.tools
	}
	count := len(usage.MCPTools) + usage.MCPToolCount
	if count == 0 {
		return box
	}
	chevron = iconChevronRight
	if state.toolsOpen {
		chevron = iconChevron
	}
	toggle := newRow(fmt.Sprintf("MCP tools · %d", count), chevron, styleChip, func() {
		state.toolsOpen = !state.toolsOpen
		if state.toolsOpen && list == nil && tools != nil {
			tools()
		}
		changed()
	})
	box.Add(padded(4, 0, 0, 0, container.NewHBox(toggle)))
	if state.toolsOpen {
		if list == nil {
			box.Add(textLine("Loading…", 12, colMuted))
		}
		names := container.New(layout.NewCustomPaddedVBoxLayout(3))
		for _, tool := range list {
			name := textLine(tool.Name, 11.5, colText)
			if !tool.Loaded {
				name.Color = colMuted
			}
			names.Add(container.NewBorder(nil, nil, name, textLine(agent.Tokens(tool.Tokens), 11.5, colMuted)))
		}
		// The footer does not scroll with the tasks: a long list scrolls on
		// its own instead of pushing the panel's top out.
		scroll := container.NewVScroll(names)
		scroll.SetMinSize(fyne.NewSize(0, min(names.MinSize().Height, 180)))
		box.Add(scroll)
	}
	return box
}
