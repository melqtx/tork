package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
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
	inCard("tab", "next screen"),
	inCard("esc", "back"),
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
			onStrip("enter", "search or open"),
			onStrip("↑↓", "menu"),
			onStrip("tab", "screens"),
			inCard("paste", "a magnet, infohash, .torrent path or URL"),
			inCard("esc", "clear the field"),
		}

	case screenISOs:
		return []keyHint{
			onStrip("↑↓", "move"),
			onStrip("enter", "download"),
			onStrip("tab", "screens"),
			onStrip("esc", "home"),
			inCard("g/G", "top / bottom"),
			inCard("pgup/pgdn", "page"),
		}

	case screenResults:
		if a.results.grouped {
			return []keyHint{
				onStrip("↑↓", "move"),
				onStrip("←→", "fold"),
				onStrip("enter", "get"),
				onStrip("v", "flat"),
				onStrip("esc", "back"),
				inCard("space", "fold this group"),
				inCard("D", "get now, skipping the preview"),
				inCard("Y", "copy magnet"),
				inCard("o", "sort: "+a.results.sort.String()),
				inCard("/", "smart filter: res:1080p seeders:>20 size:<8gb is:trusted"),
				inCard("g/G", "top / bottom"),
			}
		}
		return []keyHint{
			onStrip("↑↓", "move"),
			onStrip("enter", "get"),
			onStrip("/", "smart filter"),
			onStrip("o", a.results.sort.String()),
			onStrip("v", "graph"),
			onStrip("esc", "back"),
			inCard("D", "get now, skipping the preview"),
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
			onStrip("enter", "download selected"),
			onStrip("space", toggle),
			onStrip("←→", "fold"),
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
			onStrip("x", "remove"),
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
			return "results · graph"
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

// viewHelp is the `?` card: every key for the screen underneath, then the ones
// that work anywhere. It falls back to two columns on a short terminal, because
// this is the one place every key is written down - losing the tail off the
// bottom would defeat the point of having it.
func (a *App) viewHelp() string {
	here := helpSection(a.screenTitle(), a.screenKeys())
	everywhere := helpSection("everywhere", globalKeys)

	stacked := append(append([]string{}, here...), append([]string{""}, everywhere...)...)
	body := strings.Join(stacked, "\n")
	if len(stacked) > a.bodyHeight() {
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			strings.Join(here, "\n"), "    ", strings.Join(everywhere, "\n"))
	}
	return a.chrome("keys", body, styleDim.Render("any key closes this"))
}

func helpSection(title string, keys []keyHint) []string {
	lines := []string{styleTitle.Render(title), ""}
	for _, kh := range keys {
		lines = append(lines, "  "+padRight(styleKey.Render(kh.key), 12)+styleDim.Render(kh.label))
	}
	return lines
}
