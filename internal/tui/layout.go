package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// maxContentWidth caps the centered column so wide terminals get real margins
// (the opencode look) instead of edge-to-edge text.
const maxContentWidth = 118

func (a *App) termWidth() int {
	if a.width <= 0 {
		return 100
	}
	return a.width
}

func (a *App) termHeight() int {
	if a.height <= 0 {
		return 30
	}
	return a.height
}

// contentWidth is the width of the centered column.
func (a *App) contentWidth() int {
	w := a.termWidth() - 8
	if w > maxContentWidth {
		w = maxContentWidth
	}
	if w < 40 {
		w = 40
	}
	return w
}

// bodyHeight is the vertical space for a screen's body, between the 2-line
// header and the 2-line footer.
func (a *App) bodyHeight() int {
	h := a.termHeight() - 4
	if h < 1 {
		h = 1
	}
	return h
}

// center left-pads every line so the fixed-width column sits centered.
func (a *App) center(block string) string {
	pad := (a.termWidth() - a.contentWidth()) / 2
	if pad <= 0 {
		return block
	}
	prefix := strings.Repeat(" ", pad)
	lines := strings.Split(block, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

func rule(w int) string {
	if w < 1 {
		return ""
	}
	return styleRule.Render(strings.Repeat("─", w))
}

// padRight pads s (which may contain ANSI) with spaces to visible width w.
func padRight(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// padLines forces s to exactly n lines (truncating or padding with blanks).
func padLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// headerBar is the wordmark + context line plus an underline rule.
func (a *App) headerBar(context string) string {
	w := a.contentWidth()
	left := styleBrand.Render("tork")
	if context != "" {
		left += styleFaint.Render("  ·  ") + styleDim.Render(context)
	}
	return padRight(left, w) + "\n" + rule(w)
}

// footerBar is a rule plus a help/status line (errors take over when present).
func (a *App) footerBar(help string) string {
	w := a.contentWidth()
	line := help
	if a.errText != "" {
		line = styleErr.Render(a.errText)
	}
	return rule(w) + "\n" + padRight(line, w)
}

// chrome composes a full screen: centered column, header on top, body filling
// the middle, footer pinned to the bottom.
func (a *App) chrome(context, body, help string) string {
	body = padLines(body, a.bodyHeight())
	col := a.headerBar(context) + "\n" + body + "\n" + a.footerBar(help)
	return a.center(col)
}

// hint renders one "key label" pair; hints joins several.
func hint(key, label string) string {
	return styleKey.Render(key) + " " + styleKeyLb.Render(label)
}

func hints(parts ...string) string {
	return strings.Join(parts, styleFaint.Render("   "))
}
