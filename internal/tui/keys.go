package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// keyHint is one documented shortcut. `strip` marks the few that earn a place
// on the always-visible footer; everything else lives behind `?`.
//
// Both the footer and the help card read this one table, so the two can never
// drift apart - or away from what the key actually does. The downloads screen
// is why this exists: its footer had grown to twelve hints, ran past the
// content column, and collided with the proxy badge sharing that line.
type keyHint struct {
	key   string
	label string
	strip bool
}

func onStrip(key, label string) keyHint { return keyHint{key: key, label: label, strip: true} }
func inCard(key, label string) keyHint  { return keyHint{key: key, label: label} }

// globalKeys work from anywhere and are listed once, in the card, rather than
// eating room on every screen's footer.
var globalKeys = []keyHint{
	inCard("tab / shift+tab", "home: focus controls; elsewhere: downloads / back"),
	inCard("esc", "back or cancel current step"),
	inCard("^d", "downloads"),
	inCard("H", "health history"),
	inCard("?", "this card"),
	inCard("^c", "quit"),
}

// screenKeys is the keymap for whatever is on screen right now.
func (a *App) screenKeys() []keyHint {
	switch a.screen {
	case screenSearch:
		return []keyHint{
			onStrip("enter", a.homeEnterLabel()),
			onStrip("↑↓", "choose"),
			inCard("tab / shift+tab", "next / previous control"),
			inCard("paste", "a magnet, infohash, .torrent path or URL"),
			inCard("esc", "focus search, then clear the field"),
		}

	case screenISOs:
		return []keyHint{
			onStrip("↑↓", "move"),
			onStrip("enter", "download"),
			onStrip("tab", "downloads"),
			onStrip("esc", "back"),
			inCard("g/G", "top / bottom"),
			inCard("pgup/pgdn", "page"),
		}

	case screenResults:
		if a.results.curated {
			return []keyHint{onStrip("enter", a.resultEnterLabel()), onStrip("/", "filters"), onStrip("tab", "downloads"), onStrip("esc", "back"), inCard("↑↓", "choose a release"), inCard("v", "full list / top picks"), inCard("f", "advanced text filter"), inCard("D", "download now, skipping the preview"), inCard("Y", "copy magnet")}
		}
		if a.results.grouped {
			return []keyHint{
				inCard("↑↓", "move"),
				inCard("←→", "fold"),
				onStrip("enter", a.resultEnterLabel()),
				onStrip("tab", "downloads"),
				inCard("v", "top picks"),
				onStrip("esc", "back"),
				inCard("space", "fold this group"),
				inCard("D", "download now, skipping the preview"),
				inCard("Y", "copy magnet"),
				inCard("o", "sort: "+a.results.sort.String()),
				inCard("/", "smart filter: res:1080p seeders:>20 size:<8gb is:trusted"),
				inCard("g/G", "top / bottom"),
			}
		}
		return []keyHint{
			inCard("↑↓", "move"),
			onStrip("enter", a.resultEnterLabel()),
			onStrip("tab", "downloads"),
			onStrip("/", "filter"),
			inCard("o", "sort: "+a.results.sort.String()),
			inCard("v", "top picks"),
			inCard("V", "release groups"),
			inCard("f", "advanced text filter"),
			onStrip("esc", "back"),
			inCard("D", "download now, skipping the preview"),
			inCard("Y", "copy magnet"),
			inCard("g/G", "top / bottom"),
			inCard("pgup/pgdn", "page"),
		}

	case screenPreview:
		toggle := "toggle"
		if n := a.preview.currentNode(); n != nil && n.fileIdx < 0 {
			toggle = "toggle folder"
		}
		return []keyHint{
			onStrip("enter", a.previewDownloadLabel()),
			onStrip("space", toggle),
			inCard("←→", "fold"),
			onStrip("esc", "cancel"),
			inCard("a", "select every file"),
			inCard("n", "select none"),
			inCard("g/G", "top / bottom"),
		}

	case screenHealth:
		return []keyHint{
			onStrip("↑↓", "move"),
			onStrip("r", "re-check"),
			onStrip("esc", "back"),
			onStrip("q", "quit"),
		}

	default:
		keys := []keyHint{
			onStrip("↑↓", "move"),
			onStrip("enter", "open"),
			onStrip("p", "pause"),
			onStrip("esc", "back"),
			inCard("x", "remove"),
			inCard("s", "seed on or off"),
			inCard("v", "verify completed data"),
			inCard("m", "move to another folder"),
			inCard("r", "relink to existing files"),
			inCard("y", "copy full path"),
			inCard("Y", "copy magnet"),
			inCard("d", "delete the data too"),
			inCard("g/G", "top / bottom"),
		}
		if revealAvailable {
			keys = append(keys, inCard("o", revealLabel))
		}
		return keys
	}
}

// keyStrip renders the footer line for the current screen in the room it
// actually has, always ending with the `?` pointer so the full list is
// reachable from everywhere. On a narrow terminal the tail is dropped rather
// than run off the edge - the card holds every key anyway.
func (a *App) keyStrip(width int) string {
	const sep = 3 // hints() joins with three spaces
	var parts []string
	for _, kh := range a.screenKeys() {
		if kh.strip {
			parts = append(parts, hint(kh.key, kh.label))
		}
	}
	parts = append(parts, hint("?", "keys"))
	for len(parts) > 1 && stripWidth(parts, sep) > width {
		parts = append(parts[:len(parts)-2], parts[len(parts)-1])
	}
	return hints(parts...)
}

func stripWidth(parts []string, sep int) int {
	total := 0
	for i, p := range parts {
		total += lipgloss.Width(p)
		if i > 0 {
			total += sep
		}
	}
	return total
}

// helpBudget is the room the strip has on the footer line once the proxy badge
// sharing that line is paid for.
func (a *App) helpBudget(width int) int {
	if tail := a.proxyStatusTail(); tail != "" {
		width -= lipgloss.Width(tail) + 2
	}
	return max(20, width)
}

func (a *App) screenTitle() string {
	switch a.screen {
	case screenSearch:
		return "home"
	case screenISOs:
		return "linux isos"
	case screenResults:
		if a.results.grouped {
			return "results · grouped"
		}
		return "results"
	case screenPreview:
		return "preview"
	case screenHealth:
		return "health"
	default:
		return "downloads"
	}
}

// helpLines wraps the key reference into columns when space allows. Smaller
// windows can scroll the complete reference instead of clipping shortcuts.
func (a *App) helpLines() []string {
	here := strings.Join(helpSection(a.screenTitle(), a.screenKeys()), "\n")
	everywhere := strings.Join(helpSection("navigation", globalKeys), "\n")
	width := a.contentWidth()
	var body string
	if width >= 80 {
		leftWidth := (width - 4) / 2
		left := lipgloss.NewStyle().Width(leftWidth).Render(ansi.Wrap(here, leftWidth, ""))
		right := ansi.Wrap(everywhere, width-leftWidth-4, "")
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, "    ", right)
	} else {
		body = ansi.Wrap(here+"\n\n"+everywhere, width, "")
	}
	return strings.Split(body, "\n")
}

func (a *App) viewHelp() string {
	lines := a.helpLines()
	offset := min(a.helpOffset, max(0, len(lines)-a.bodyHeight()))
	help := "esc close"
	if len(lines) > a.bodyHeight() {
		help = "↑↓ / pgup/pgdn scroll · esc close"
	}
	return a.chrome("keys", strings.Join(lines[offset:], "\n"), styleDim.Render(help))
}

func helpSection(title string, keys []keyHint) []string {
	lines := []string{styleTitle.Render(title), ""}
	for _, kh := range keys {
		lines = append(lines, "  "+padRight(styleKey.Render(kh.key), 16)+styleDim.Render(kh.label))
	}
	return lines
}

func (a *App) homeEnterLabel() string {
	if a.search.menuFocused {
		return "open " + a.homeDestinations()[a.search.menu].name
	}
	return "search"
}

func (a *App) resultEnterLabel() string {
	if a.cfg != nil && !a.cfg.PreviewBeforeDownload {
		return "download"
	}
	return "preview"
}

func (a *App) previewDownloadLabel() string {
	if !a.preview.ready {
		return "queue all"
	}
	if a.preview.selectedBytes() == 0 {
		return "select files first"
	}
	return "download selected"
}
