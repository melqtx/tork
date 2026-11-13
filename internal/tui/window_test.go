package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
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

// A colored cell used to reset the selection background for every later column.
// Count the cells that actually carry a background, including trailing padding.
func TestRenderWindowHighlightsEntireSelectedRow(t *testing.T) {
	oldProfile, oldDark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(oldProfile)
		lipgloss.SetHasDarkBackground(oldDark)
	})
	for _, profile := range []termenv.Profile{termenv.ANSI256, termenv.TrueColor} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{1, 20, 80, 160} {
				t.Run(fmt.Sprintf("profile=%d/dark=%v/width=%d", profile, dark, width), func(t *testing.T) {
					lipgloss.SetColorProfile(profile)
					lipgloss.SetHasDarkBackground(dark)
					w := listWindow{cursor: 1}
					out := renderWindow(&w, 3, 3, width, func(_ int, _ bool) string {
						return styleFg.Render("Linux 界") + "  " + styleDim.Render("2.0 GiB") + "  " + styleSeeders.Render("42") + "  " + providerTag("knaben")
					})
					for row, line := range strings.Split(out, "\n") {
						if got := lipgloss.Width(line); got != width {
							t.Fatalf("row %d width = %d, want %d", row, got, width)
						}
						want := 0
						if row == 1 {
							want = width
						}
						if got := backgroundCells(line); got != want {
							t.Fatalf("row %d background covers %d cells, want %d: %q", row, got, want, line)
						}
					}
				})
			}
		}
	}
}

func TestResultViewsHighlightEntireRow(t *testing.T) {
	oldProfile, oldDark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(oldProfile)
		lipgloss.SetHasDarkBackground(oldDark)
	})
	for _, profile := range []termenv.Profile{termenv.ANSI256, termenv.TrueColor} {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{80, 160} {
				for _, mode := range []string{"browse", "flat", "grouped", "picks"} {
					t.Run(fmt.Sprintf("%s/width=%d/dark=%v/profile=%d", mode, width, dark, profile), func(t *testing.T) {
						lipgloss.SetColorProfile(profile)
						lipgloss.SetHasDarkBackground(dark)
						a := browseTestApp()
						a.width, a.height = width, 30
						a.results.browser = mode == "browse"
						a.results.curated = mode == "picks"
						a.results.grouped = mode == "grouped"
						a.results.refreshFilter()
						wantWidth := a.contentWidth()
						if mode == "browse" {
							wantWidth = a.browseWidth()
						}
						previous := ""
						for step := 0; step < 2; step++ {
							selected := ""
							for _, line := range strings.Split(a.View(), "\n") {
								plain := ansi.Strip(line)
								if strings.Contains(plain, "▍") {
									if selected != "" {
										t.Fatal("multiple result rows are highlighted")
									}
									selected = plain
									if got := backgroundCells(line); got != wantWidth {
										t.Fatalf("background covers %d cells, want %d: %q", got, wantWidth, line)
									}
								} else if backgroundCells(line) != 0 {
									t.Fatalf("highlight leaked onto another row: %q", line)
								}
							}
							if selected == "" || selected == previous {
								t.Fatal("highlight did not follow the selected result")
							}
							previous = selected
							a.Update(tea.KeyMsg{Type: tea.KeyDown})
						}
					})
				}
			}
		}
	}
}

// Interpret SGR state so a reset between columns cannot pass the test.
func backgroundCells(line string) int {
	sgr := regexp.MustCompile(`\x1b\[([0-9;]*)m`)
	background, cells, position := false, 0, 0
	for _, match := range sgr.FindAllStringSubmatchIndex(line, -1) {
		if background {
			cells += lipgloss.Width(line[position:match[0]])
		}
		params := strings.Split(line[match[2]:match[3]], ";")
		for i := 0; i < len(params); i++ {
			switch params[i] {
			case "", "0", "49":
				background = false
			case "38", "48":
				if params[i] == "48" {
					background = true
				}
				// Color indices and RGB components are not SGR commands.
				if i+1 < len(params) {
					if params[i+1] == "5" {
						i += 2
					} else if params[i+1] == "2" {
						i += 4
					}
				}
			}
		}
		position = match[1]
	}
	if background {
		cells += lipgloss.Width(line[position:])
	}
	return cells
}
