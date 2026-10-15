package main

import (
	"context"
	"fmt"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"regexp"
	"strings"
)

var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78DEHMc]`)

// plainTerminal turns a terminal's raw output into text: no escape
// sequences, and a line rewritten after a carriage return shows its end.
func plainTerminal(raw []byte) string {
	text := ansiSequence.ReplaceAllString(string(raw), "")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if cut := strings.LastIndex(strings.TrimRight(line, "\r"), "\r"); cut >= 0 {
			line = line[cut+1:]
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' || r >= ' ' {
				return r
			}
			return -1
		}, line)
	}
	return strings.Join(lines, "\n")
}

func browserInputFromPhone(ctx context.Context, b *browserPage, action string, x, y, dy float64, text string) error {
	info := b.snapshot()
	px, py := x*float64(info.Width), y*float64(info.Height)
	switch action {
	case "tap":
		b.input(input.DispatchMouseEvent(input.MouseMoved, px, py))
		b.input(input.DispatchMouseEvent(input.MousePressed, px, py).WithButton(input.Left).WithClickCount(1))
		b.input(input.DispatchMouseEvent(input.MouseReleased, px, py).WithButton(input.Left).WithClickCount(1))
	case "scroll":
		b.input(input.DispatchMouseEvent(input.MouseWheel, px, py).WithDeltaY(dy * float64(info.Height)))
	case "text", "line":
		if text != "" {
			b.input(input.InsertText(text))
		}
		if action == "line" {
			b.input(chromedp.KeyEvent(kb.Enter))
		}
	case "key":
		key := map[string]string{"Enter": kb.Enter, "Backspace": kb.Backspace, "Tab": kb.Tab, "Escape": kb.Escape}[text]
		if key == "" {
			return fmt.Errorf("Unknown key")
		}
		b.input(chromedp.KeyEvent(key))
	case "navigate", "back", "forward", "reload":
		return b.navigateSoon(action, text)
	default:
		return fmt.Errorf("Unknown browser action")
	}
	return nil
}
