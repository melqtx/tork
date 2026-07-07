package tui

import (
	"strings"
	"testing"
)

func TestListWindowMove(t *testing.T) {
	tests := []struct {
		name           string
		total, visible int
		deltas         []int
		wantCursor     int
		wantOffset     int
	}{
		{"down within window", 10, 3, []int{1, 1}, 2, 0},
		{"down past window scrolls", 10, 3, []int{1, 1, 1}, 3, 1},
		{"page down clamps at end", 10, 3, []int{20}, 9, 7},
		{"up from top stays", 10, 3, []int{-5}, 0, 0},
		{"empty list", 0, 3, []int{1, -1}, 0, 0},
		{"total smaller than window", 2, 5, []int{10}, 1, 0},
		{"single visible row", 5, 1, []int{1, 1}, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w listWindow
			for _, d := range tt.deltas {
				w.move(d, tt.total, tt.visible)
			}
			if w.cursor != tt.wantCursor || w.offset != tt.wantOffset {
				t.Errorf("cursor/offset = %d/%d, want %d/%d", w.cursor, w.offset, tt.wantCursor, tt.wantOffset)
			}
		})
	}
}

func TestListWindowClampAfterShrink(t *testing.T) {
	w := listWindow{cursor: 8, offset: 6}
	start, end := w.clamp(4, 3) // list shrank under the cursor
	if w.cursor != 3 {
		t.Errorf("cursor = %d, want 3", w.cursor)
	}
	if start != 1 || end != 4 {
		t.Errorf("window = [%d,%d), want [1,4)", start, end)
	}
}

func TestListWindowHomeEnd(t *testing.T) {
	var w listWindow
	w.end(10, 3)
	if w.cursor != 9 || w.offset != 7 {
		t.Errorf("end: cursor/offset = %d/%d, want 9/7", w.cursor, w.offset)
	}
	w.home()
	if w.cursor != 0 || w.offset != 0 {
		t.Errorf("home: cursor/offset = %d/%d, want 0/0", w.cursor, w.offset)
	}
}

func TestRenderWindowPadsAndSelects(t *testing.T) {
	w := listWindow{cursor: 1}
	out := renderWindow(&w, 2, 4, 20, func(i int, selected bool) string {
		if selected {
			return "sel"
		}
		return "row"
	})
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4 (padded)", len(lines))
	}
	if !strings.Contains(lines[0], "row") {
		t.Errorf("line 0 = %q, want unselected row", lines[0])
	}
	if !strings.Contains(lines[1], "▍") || !strings.Contains(lines[1], "sel") {
		t.Errorf("line 1 = %q, want selection bar + selected row", lines[1])
	}
	if lines[2] != "" || lines[3] != "" {
		t.Errorf("padding lines not empty: %q, %q", lines[2], lines[3])
	}
}
