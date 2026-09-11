package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

func (a *App) openPickFilters() tea.Cmd {
	r := &a.results
	r.pickPanel = true
	r.pickDraft = r.pickPrefs
	r.panelWin.home()
	return nil
}
func (a *App) updatePickFilters(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	switch msg.String() {
	case "esc":
		r.pickPanel = false
	case "enter":
		r.pickPrefs = r.pickDraft
		r.pickPanel = false
		r.refreshFilter()
	case "up", "k":
		r.panelWin.move(-1, 9, max(1, a.bodyHeight()-4))
	case "down", "j":
		r.panelWin.move(1, 9, max(1, a.bodyHeight()-4))
	case " ", "right", "left":
		i := r.panelWin.cursor
		switch {
		case i < 6:
			// zero means all; the first toggle starts an explicit selection.
			r.pickDraft.resolutions ^= 1 << uint(resolutionOrder[i])
		case i == 6:
			delta := 1
			if msg.String() == "left" {
				delta = -1
			}
			r.pickDraft.size = (r.pickDraft.size + delta + len(pickSizes)) % len(pickSizes)
		case i == 7:
			delta := 1
			if msg.String() == "left" {
				delta = -1
			}
			r.pickDraft.preference = (r.pickDraft.preference + delta + len(pickModes)) % len(pickModes)
		case i == 8:
			r.pickDraft = pickPreferences{}
		}
	}
	return a, nil
}
func (a *App) viewPickFilters() string {
	r := &a.results
	w := a.contentWidth()
	body := styleTitle.Render("Filter results") + "\n" + styleDim.Render("Choose any resolutions. None selected means all.") + "\n\n"
	body += renderWindow(&r.panelWin, 9, max(1, a.bodyHeight()-4), w, func(i int, selected bool) string {
		if i < 6 {
			box := "[ ]"
			if r.pickDraft.resolutions&(1<<uint(resolutionOrder[i])) != 0 {
				box = "[✓]"
			}
			return box + " " + resolutionName(resolutionOrder[i])
		}
		if i == 6 {
			size := "Any size"
			if pickSizes[r.pickDraft.size] > 0 {
				size = "≤ " + humanBytes(pickSizes[r.pickDraft.size]) + " (known sizes only)"
			}
			return "Max size     " + size
		}
		if i == 7 {
			return "Prefer       " + pickModes[r.pickDraft.preference]
		}
		return "Reset resolution, size and preference"
	})
	if s := strings.TrimSpace(r.filterIn.Value()); s != "" {
		body += "\n" + styleDim.Render(fmt.Sprintf("Advanced filter also active: %s", s))
	}
	return a.chrome("results", body, hints(hint("space / ←→", "change"), hint("enter", "apply"), hint("esc", "cancel")))
}
