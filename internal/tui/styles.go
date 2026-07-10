package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette - a restrained, cozy dark theme: one soft green accent, layered
// grays, and calm semantic colors. Green for the app's own voice; amber/rose
// reserved for caution/alarm so signal never blends into the accent.
var (
	colBrand  = lipgloss.Color("114") // soft green - the accent
	colBrand2 = lipgloss.Color("108") // sage green - secondary (keys, borders)
	colFg     = lipgloss.Color("253")
	colText   = lipgloss.Color("250")
	colMuted  = lipgloss.Color("245")
	colFaint  = lipgloss.Color("240")
	colBorder = lipgloss.Color("238")
	colSel    = lipgloss.Color("236") // selection background
	colGreen  = lipgloss.Color("114") // seeders / healthy - the positive green
	colAmber  = lipgloss.Color("179") // caution - mid-health, reserved for signal
	colRose   = lipgloss.Color("174")
	colBlue   = lipgloss.Color("111")
	colViolet = lipgloss.Color("176")
)

var (
	styleBrand = lipgloss.NewStyle().Bold(true).Foreground(colBrand)
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colBrand)
	styleFg    = lipgloss.NewStyle().Foreground(colFg)
	styleDim   = lipgloss.NewStyle().Foreground(colMuted)
	styleFaint = lipgloss.NewStyle().Foreground(colFaint)
	styleRule  = lipgloss.NewStyle().Foreground(colBorder)

	styleSelected = lipgloss.NewStyle().Background(colSel).Foreground(colFg).Bold(true)
	styleSelBar   = lipgloss.NewStyle().Foreground(colBrand)

	styleSeeders  = lipgloss.NewStyle().Foreground(colGreen)
	styleLeechers = lipgloss.NewStyle().Foreground(colRose)
	styleErr      = lipgloss.NewStyle().Foreground(colRose).Bold(true)
	styleOK       = lipgloss.NewStyle().Foreground(colGreen)
	styleMatch    = lipgloss.NewStyle().Foreground(colBrand).Bold(true)
	styleBest     = lipgloss.NewStyle().Foreground(colAmber).Bold(true) // best source - gold, distinct from the green
	styleStateTag = lipgloss.NewStyle().Foreground(colBlue)
	styleHelp     = lipgloss.NewStyle().Foreground(colFaint)

	styleKey   = lipgloss.NewStyle().Foreground(colBrand2)
	styleKeyLb = lipgloss.NewStyle().Foreground(colMuted)
)

var (
	styleHealthGood = lipgloss.NewStyle().Foreground(colGreen)
	styleHealthMid  = lipgloss.NewStyle().Foreground(colAmber)
	styleHealthBad  = lipgloss.NewStyle().Foreground(colRose)
)

// healthDot returns a swarm-health indicator colored by seeder count.
func healthDot(seeders int) string {
	switch {
	case seeders >= 50:
		return styleHealthGood.Render("●")
	case seeders >= 5:
		return styleHealthMid.Render("●")
	case seeders >= 1:
		return styleHealthBad.Render("●")
	}
	return styleFaint.Render("○")
}

var providerStyles = map[string]lipgloss.Style{
	"knaben":     lipgloss.NewStyle().Foreground(colBrand),
	"yts":        lipgloss.NewStyle().Foreground(colGreen),
	"nyaa":       lipgloss.NewStyle().Foreground(colViolet),
	"tpb-movies": lipgloss.NewStyle().Foreground(colBrand2),
	"tpb-tv":     lipgloss.NewStyle().Foreground(colBrand2),
	"1337x":      lipgloss.NewStyle().Foreground(colRose),
	"eztv":       lipgloss.NewStyle().Foreground(colBlue),
}

func providerTag(name string) string {
	if s, ok := providerStyles[name]; ok {
		return s.Render(name)
	}
	return lipgloss.NewStyle().Foreground(colMuted).Render(name)
}

// providerBracket renders a provider as a small tag: [knaben].
func providerBracket(name string) string {
	return styleFaint.Render("[") + providerTag(name) + styleFaint.Render("]")
}

// torkLogo is a clean, flat half-block wordmark - minimal and calm, in the
// spirit of a modern CLI rather than a heavy retro marquee.
var torkLogo = []string{
	`▀█▀ █▀█ █▀█ █▄▀`,
	` █  █▄█ █▀▄ █▀▄`,
}

// logoGradient tints the wordmark top-to-bottom: a soft wash from pale mint
// down through sage to a deep forest green, so the banner glows rather than
// shouts - minimal, but cozy.
var logoGradient = []lipgloss.Color{
	lipgloss.Color("157"),
	lipgloss.Color("151"),
	lipgloss.Color("114"),
	lipgloss.Color("108"),
	lipgloss.Color("72"),
	lipgloss.Color("65"),
}

// renderLogo paints torkLogo top-to-bottom, spreading the gradient evenly
// across however many rows the wordmark has.
func renderLogo() string {
	n := len(torkLogo)
	lines := make([]string, n)
	for i, l := range torkLogo {
		idx := 0
		if n > 1 {
			idx = i * (len(logoGradient) - 1) / (n - 1)
		}
		lines[i] = lipgloss.NewStyle().Bold(true).Foreground(logoGradient[idx]).Render(l)
	}
	return strings.Join(lines, "\n")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func humanSpeed(bps float64) string {
	if bps <= 0 {
		return "0 B/s"
	}
	return humanBytes(int64(bps)) + "/s"
}

func fmtETA(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	d = d.Round(time.Second)
	if d > 99*time.Hour {
		return "∞"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// truncate shortens s to at most w display cells (ANSI- and wide-rune-aware),
// ending with an ellipsis when anything was cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return ansi.Truncate(s, 1, "")
	}
	return ansi.Truncate(s, w, "…")
}
