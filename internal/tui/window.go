package tui

import "strings"

// listWindow owns the cursor+offset pair of a scrolling list, replacing the
// per-view copies of the same clamp-and-follow logic.
type listWindow struct {
	cursor int
	offset int
}

// move shifts the cursor by delta within [0,total) and keeps it visible.
func (w *listWindow) move(delta, total, visible int) {
	w.cursor += delta
	w.clamp(total, visible)
}

func (w *listWindow) home() { w.cursor, w.offset = 0, 0 }

func (w *listWindow) end(total, visible int) {
	w.cursor = total - 1
	w.clamp(total, visible)
}

// clamp normalizes cursor and offset against the current list and window
// sizes, returning the visible range [start, end).
func (w *listWindow) clamp(total, visible int) (start, end int) {
	if visible < 1 {
		visible = 1
	}
	w.cursor = max(0, min(total-1, w.cursor))
	if w.cursor < w.offset {
		w.offset = w.cursor
	}
	if w.cursor >= w.offset+visible {
		w.offset = w.cursor - visible + 1
	}
	w.offset = max(0, min(w.offset, max(0, total-visible)))
	return w.offset, min(total, w.offset+visible)
}

// renderWindow draws the rows visible through w, one line each: a ▍ gutter and
// the selection background on the cursor row, padded to exactly visible lines
// so screen bodies keep a stable height. render must not include the gutter.
func renderWindow(w *listWindow, total, visible, width int, render func(i int, selected bool) string) string {
	start, end := w.clamp(total, visible)
	var b strings.Builder
	for i := start; i < end; i++ {
		if i == w.cursor {
			line := styleSelBar.Render("▍") + render(i, true)
			b.WriteString(styleSelected.Render(padRight(line, width)))
		} else {
			b.WriteString(" " + render(i, false))
		}
		b.WriteString("\n")
	}
	for i := end - start; i < visible; i++ {
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}
