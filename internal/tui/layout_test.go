package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/melqtx/tork/internal/engine"
)

func assertRenderFits(t *testing.T, view string, width int) {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line %d is %d cells wide, terminal is %d: %q", i+1, got, width, line)
		}
	}
}

func TestTruncateDisplayWidth(t *testing.T) {
	tests := []struct {
		in        string
		w         int
		wantWidth int // max display width of the result
		cut       bool
	}{
		{"hello", 10, 5, false},
		{"hello world", 8, 8, true},
		{"日本語のタイトル", 5, 5, true}, // wide runes: 2 cells each
		{"🐱🐱🐱", 3, 3, true},
		{"abc", 0, 0, true},
		{"abc", 1, 1, true},
	}
	for _, tt := range tests {
		got := truncate(tt.in, tt.w)
		if w := lipgloss.Width(got); w > tt.wantWidth {
			t.Errorf("truncate(%q, %d) = %q, display width %d > %d", tt.in, tt.w, got, w, tt.wantWidth)
		}
		if tt.cut && tt.w > 1 && !strings.HasSuffix(got, "…") {
			t.Errorf("truncate(%q, %d) = %q, want ellipsis suffix", tt.in, tt.w, got)
		}
	}
}

func TestFlexW(t *testing.T) {
	if got := flexW(100, 20, 40); got != 60 {
		t.Errorf("flexW(100,20,40) = %d, want 60", got)
	}
	if got := flexW(50, 20, 40); got != 20 {
		t.Errorf("flexW(50,20,40) = %d, want floor 20", got)
	}
	if got := flexW(100, 10, 30, 20, 10); got != 40 {
		t.Errorf("flexW with multiple fixed = %d, want 40", got)
	}
}

func TestFooterLineRightAligned(t *testing.T) {
	a := &App{width: 100}
	out := a.footerLine(40, "help", "right")
	if w := lipgloss.Width(out); w != 40 {
		t.Errorf("footer width = %d, want 40", w)
	}
	if !strings.HasPrefix(out, "help") || !strings.HasSuffix(out, "right") {
		t.Errorf("footer = %q, want help…right", out)
	}
}

func TestFooterLineErrorWins(t *testing.T) {
	a := &App{width: 100, errText: "boom"}
	out := a.footerLine(40, "help", "")
	if !strings.Contains(out, "boom") || strings.Contains(out, "help") {
		t.Errorf("footer = %q, want error to replace help", out)
	}
}

func TestChromeFitsNarrowTerminalsAndLongDynamicText(t *testing.T) {
	for _, width := range []int{8, 20, 32, 47, 48, 80} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			a := &App{width: width, height: 14, screen: screenDownloads}
			a.downloads = newDownloadsModel()
			a.downloads.snaps = []engine.Snapshot{{
				State: engine.StateDownloading, SpeedBps: 987_654_321,
			}}
			a.errText = strings.Repeat("provider failure with a very long explanation ", 4)
			a.toast = toastState{text: strings.Repeat("queued a very long torrent title ", 4)}

			view := a.chrome(
				strings.Repeat("long search context ", 8),
				strings.Repeat("a body row that came from an untrusted external provider ", 8),
				strings.Repeat("key hints ", 12),
			)
			assertRenderFits(t, view, width)
			if got := len(strings.Split(view, "\n")); got != a.termHeight() {
				t.Fatalf("rendered %d lines, want terminal height %d", got, a.termHeight())
			}
		})
	}
}

func TestHomeAndWindowFitNarrowTerminals(t *testing.T) {
	for _, width := range []int{8, 20, 32, 47} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			a := &App{width: width, height: 14, screen: screenSearch}
			a.search = newSearchModel()
			a.downloads = newDownloadsModel()
			a.search.input.SetValue(strings.Repeat("very long search query ", 8))
			assertRenderFits(t, a.viewSearch(), width)

			win := listWindow{}
			rows := renderWindow(&win, 2, 3, width, func(int, bool) string {
				return strings.Repeat("wide result title ", 8)
			})
			assertRenderFits(t, rows, width)
		})
	}
}

func TestHomeHeroIsVerticallyCentered(t *testing.T) {
	a := &App{width: 100, height: 40, screen: screenSearch}
	a.search = newSearchModel()
	a.downloads = newDownloadsModel()
	lines := strings.Split(a.viewSearch(), "\n")
	row := -1
	for i, line := range lines {
		if strings.Contains(line, "you name it, the cat fetches it") {
			row = i
			break
		}
	}
	if row < a.height/3 || row > 2*a.height/3 {
		t.Fatalf("home tagline rendered on row %d of %d, want the hero in the visual center", row, a.height)
	}
}

func TestContextHeaderHasNoFunctionKeyToolbar(t *testing.T) {
	for _, width := range []int{84, 100, 144} {
		a := navigationApp()
		a.width = width
		a.screen = screenResults
		header := a.headerBar("results · " + strings.Repeat("long query ", 30))
		for _, want := range []string{"F1 home", "F2 results", "F3 downloads", "F4 sources"} {
			if strings.Contains(header, want) {
				t.Fatalf("unwanted toolbar %q at width %d", want, width)
			}
		}
		assertRenderFits(t, header, width)
		a.screen = screenSearch
		view := a.View()
		if strings.Contains(strings.Split(view, "\n")[0], "home") {
			t.Fatal("redundant home header remains")
		}
		if len(strings.Split(view, "\n")) != a.height {
			t.Fatal("home header removal changed terminal row count")
		}
	}
}

func TestHomeTransferActivityDoesNotHideHelp(t *testing.T) {
	for _, width := range []int{40, 60, 84, 120} {
		a := navigationApp()
		a.width = width
		a.downloads.snaps = []engine.Snapshot{{State: engine.StateDownloading, SpeedBps: 12345678}}
		lines := strings.Split(a.View(), "\n")
		if !strings.Contains(lines[len(lines)-1], "? keys") {
			t.Fatalf("home activity clipped help at width %d", width)
		}
	}
}
