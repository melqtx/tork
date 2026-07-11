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

// padLeft right-aligns s (which may contain ANSI) within visible width w.
func padLeft(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return strings.Repeat(" ", gap) + s
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

// footerLine renders one status row: help text (an error, when present, takes
// over) padded to width, with an optional right-aligned tail.
func (a *App) footerLine(width int, help, right string) string {
	line := help
	if a.errText != "" {
		line = styleErr.Render(a.errText)
	}
	if right == "" {
		return padRight(line, width)
	}
	gap := width - lipgloss.Width(line) - lipgloss.Width(right)
	return line + strings.Repeat(" ", max(1, gap)) + right
}

// footerBar is a rule plus a help/status line (errors take over when present).
func (a *App) footerBar(help string) string {
	w := a.contentWidth()
	return rule(w) + "\n" + a.footerLine(w, help, a.proxyStatusTail())
}

// chrome composes a full screen: centered column, header on top, body filling
// the middle, footer pinned to the bottom.
func (a *App) chrome(context, body, help string) string {
	body = padLines(body, a.bodyHeight())
	col := a.headerBar(context) + "\n" + body + "\n" + a.footerBar(help)
	return a.center(col)
}

// flexW returns the width left for a flexible column after the fixed columns
// (and separators) are taken out, floored so narrow terminals never produce
// unusable columns.
func flexW(total, floor int, fixed ...int) int {
	w := total
	for _, f := range fixed {
		w -= f
	}
	return max(floor, w)
}

// Per-view column layouts: the fixed widths are named once so header and row
// rendering can't drift apart, with one flexible column absorbing the rest.

// resultsLayout: gutter dot title · size · S · L · res · provider.
type resultsLayout struct {
	titleW, sizeW, seedW, leechW, resW int
}

func newResultsLayout(width int) resultsLayout {
	l := resultsLayout{sizeW: 11, seedW: 5, leechW: 5, resW: 5}
	l.titleW = flexW(width, 20, 40)
	return l
}

// previewLayout: gutter box icon name · risk · bar · size.
type previewLayout struct {
	nameW, barW, sizeW int
}

func newPreviewLayout(width int) previewLayout {
	l := previewLayout{barW: 8, sizeW: 10}
	l.nameW = flexW(width, 16, 1+2+2+2+l.barW+1+l.sizeW)
	return l
}

// graphLayout is one shared column grid for every graph row: an arrow/guide,
// a flexing title (badges like the gold "best" trail inside it), then fixed
// decision columns. Headers, expanded leaves, and single-source rows all
// render through cols() so their columns line up.
//
//	 arrow  title…                        src  provider  seeds  meter  size
//	▾ interstellar 2014 · 1080p  best     ×4   [yts]     S208   ▮▮▮▯▯  2.1 GiB
type graphLayout struct {
	titleW, srcW, provW, seedW, meterW, sizeW int
}

func newGraphLayout(width int) graphLayout {
	// provW holds a bracketed tag [tpb-movies] (10 + 2 brackets).
	l := graphLayout{srcW: 3, provW: 12, seedW: 6, sizeW: 9}
	l.meterW = graphBarCells(width) // 5, or 0 on narrow terminals
	// Each row is drawn after renderWindow's 1-col selection gutter, so reserve
	// it here: gutter ' ' arrow ' ' title ' ' cols; cols joins cells with spaces.
	fixed := 1 + 1 + 1 + 1 + l.srcW + 1 + l.provW + 1 + l.seedW + 1 + l.sizeW
	if l.meterW > 0 {
		fixed += l.meterW + 1
	}
	l.titleW = flexW(width, 20, fixed)
	return l
}

// cols pads the fixed decision columns into one aligned block. Empty strings
// render as blank cells so headers and flat rows still line up with the
// expanded leaves that fill every column.
func (l graphLayout) cols(src, prov, seed, meter, size string) string {
	cells := []string{
		padRight(src, l.srcW),
		padRight(prov, l.provW),
		padRight(seed, l.seedW),
	}
	if l.meterW > 0 {
		cells = append(cells, padRight(meter, l.meterW))
	}
	cells = append(cells, padLeft(size, l.sizeW))
	return strings.Join(cells, " ")
}

// isosLayout: gutter tag name · edition · blurb.
type isosLayout struct {
	nameW, edW, blurbW int
}

func newISOsLayout(width int) isosLayout {
	l := isosLayout{nameW: 15, edW: 26}
	l.blurbW = flexW(width, 10, 1+2+l.nameW+l.edW)
	return l
}

// hint renders one "key label" pair; hints joins several.
func hint(key, label string) string {
	return styleKey.Render(key) + " " + styleKeyLb.Render(label)
}

func hints(parts ...string) string {
	return strings.Join(parts, styleFaint.Render("   "))
}
