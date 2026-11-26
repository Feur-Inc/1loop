// SPDX-License-Identifier: MPL-2.0

package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"

	"1loop/internal/platform"
)

// auditSlack is how far a row's slot may differ from what its content
// needs before the developer log names it (#497 gaps, #440 overlaps).
const auditSlack = 3

// audit checks, in the developer log only, the rows just laid out: each
// one's slot is the height its content needs, it sits where the running
// totals say, its boxes leave no hole between their parts, and its text
// is drawn at the width it was laid out at. Each finding is logged once,
// naming the row, so that the next gap says where it comes from.
func (layoutSpec *transcriptLayout) audit(lo, hi int, width float32) {
	if platform.Dev == nil {
		return
	}
	if layoutSpec.reported == nil {
		layoutSpec.reported = map[fyne.CanvasObject]string{}
	}
	for i := lo; i < hi && i < len(layoutSpec.rows); i++ {
		object := layoutSpec.rows[i].object
		if !object.Visible() {
			continue
		}
		report := layoutSpec.auditRow(i, width)
		if report == layoutSpec.reported[object] {
			continue
		}
		layoutSpec.reported[object] = report
		if report != "" {
			platform.Devf("layout", "row %d of %d (%s): %s", i, len(layoutSpec.rows), describeRow(object), report)
		}
	}
}

// auditRow is what is wrong with row i, shown at width; "" when nothing.
func (layoutSpec *transcriptLayout) auditRow(i int, width float32) string {
	// Inside a frame's pass the minimum sizes are remembered: measure
	// afresh, or a stale one would agree with the stale slot.
	fyne.ForgetMinSizes()
	row := layoutSpec.rows[i]
	object := row.object
	var problems []string
	if need := object.MinSize().Height; abs32(need-row.size.Height) > auditSlack {
		problems = append(problems, fmt.Sprintf("slot %.0f high, content needs %.0f", row.size.Height, need))
	}
	if abs32(object.Size().Height-row.size.Height) > auditSlack {
		problems = append(problems, fmt.Sprintf("drawn %.0f high, slot %.0f", object.Size().Height, row.size.Height))
	}
	if y := object.Position().Y; abs32(y-layoutSpec.tops[i]) > auditSlack {
		if _, rising := layoutSpec.arrivals[object]; !rising {
			problems = append(problems, fmt.Sprintf("at y %.0f, expected %.0f", y, layoutSpec.tops[i]))
		}
	}
	if row.width != width {
		problems = append(problems, fmt.Sprintf("measured at width %.0f, shown at %.0f", row.width, width))
	}
	return strings.Join(auditParts(object, problems), "; ")
}

// auditParts finds, inside a row, a vertical box whose parts are laid out
// higher or lower than they need, and text laid out or drawn at another
// width than its own: a hole, or blank lines, in a row whose slot is right.
func auditParts(object fyne.CanvasObject, problems []string) []string {
	if !object.Visible() {
		return problems
	}
	switch object := object.(type) {
	case *flowText:
		switch size := object.Size(); {
		case size.Width > 0 && object.width != size.Width:
			problems = append(problems, fmt.Sprintf("text laid out at width %.0f, its box is %.0f", object.width, size.Width))
		case size.Width > 0 && !object.measuring && object.paintedWidth != object.width:
			problems = append(problems, fmt.Sprintf("text not drawn (drawn at width %.0f, laid out at %.0f)", object.paintedWidth, object.width))
		}
	case *fyne.Container:
		vertical := fmt.Sprintf("%T", object.Layout) == "*layout.vBoxLayout"
		for _, child := range object.Objects {
			if !child.Visible() {
				continue
			}
			if vertical {
				if need, has := child.MinSize().Height, child.Size().Height; abs32(need-has) > auditSlack {
					problems = append(problems, fmt.Sprintf("a %T inside is %.0f high, needs %.0f", child, has, need))
				}
			}
			problems = auditParts(child, problems)
		}
	}
	return problems
}

// describeRow names a row for the log: a tool call by its tool, anything
// else by the start of its first text.
func describeRow(object fyne.CanvasObject) string {
	if row, ok := object.(*toolRow); ok {
		return fmt.Sprintf("tool %s, item %d", row.item.Tool, row.index)
	}
	if text := firstText(object); text != "" {
		return fmt.Sprintf("%T %q", object, platform.DevText(text, 48))
	}
	return fmt.Sprintf("%T", object)
}

func firstText(object fyne.CanvasObject) string {
	switch object := object.(type) {
	case *flowText:
		var text strings.Builder
		for _, part := range object.spans {
			text.WriteString(part.text)
		}
		return text.String()
	case *fyne.Container:
		for _, child := range object.Objects {
			if text := firstText(child); text != "" {
				return text
			}
		}
	}
	return ""
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
