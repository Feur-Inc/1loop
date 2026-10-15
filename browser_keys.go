package main

import "github.com/chromedp/chromedp/kb"

func browserNamedKey(key string) string {
	switch key {
	case "Return", "Enter", "KP_Enter":
		return kb.Enter
	case "Tab":
		return kb.Tab
	case "BackSpace":
		return kb.Backspace
	case "Delete":
		return kb.Delete
	case "Escape":
		return kb.Escape
	case "Left":
		return kb.ArrowLeft
	case "Right":
		return kb.ArrowRight
	case "Up":
		return kb.ArrowUp
	case "Down":
		return kb.ArrowDown
	case "Home":
		return kb.Home
	case "End":
		return kb.End
	case "Prior":
		return kb.PageUp
	case "Next":
		return kb.PageDown
	}
	return ""
}
