package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

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
