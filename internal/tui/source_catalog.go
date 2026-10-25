package tui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/sources"
	"strings"
)

type sourceCatalog struct {
	filter textinput.Model
	win    listWindow
}

func (a *App) openSourceCatalog() tea.Cmd {
	in := textinput.New()
	in.Placeholder = "find by name, e.g. LimeTorrents"
	in.Prompt = "Find: "
	in.CharLimit = 80
	in.PromptStyle, in.TextStyle = styleBrand, styleFg
	a.sources.catalog = &sourceCatalog{filter: in}
	return a.sources.catalog.filter.Focus()
}

func (a *App) catalogSources() []sources.Source {
	all := sources.Available(a.cfg)
	query := strings.Fields(strings.ToLower(a.sources.catalog.filter.Value()))
	var filtered []sources.Source
	for _, s := range all {
		text := strings.ToLower(s.Label + " " + sources.Description(s))
		match := true
		for _, word := range query {
			if !strings.Contains(text, word) {
				match = false
				break
			}
		}
		if match {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func (a *App) catalogRows() int { return max(1, a.bodyHeight()-7) }

func (a *App) updateSourceCatalog(msg tea.Msg) (tea.Model, tea.Cmd) {
	c := a.sources.catalog
	if a.sources.saving != "" {
		return a, nil
	}
	list := a.catalogSources()
	total := len(list) + 1 // the explicit advanced option remains reachable
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			a.sources.catalog = nil
			return a, nil
		case "up", "shift+tab":
			c.win.move(-1, total, a.catalogRows())
			return a, nil
		case "down", "tab":
			c.win.move(1, total, a.catalogRows())
			return a, nil
		case "pgup":
			c.win.move(-a.catalogRows(), total, a.catalogRows())
			return a, nil
		case "pgdown":
			c.win.move(a.catalogRows(), total, a.catalogRows())
			return a, nil
		case "enter":
			c.win.clamp(total, a.catalogRows())
			if c.win.cursor == len(list) {
				a.sources.catalog = nil
				return a, a.openSourceEditor(nil)
			}
			s, cfg := list[c.win.cursor], a.cfg
			if _, err := sources.Build(s.ID, s.Config, cfg.ProxyHTTPClient()); err != nil {
				return a, a.showError("cannot add source: " + err.Error())
			}
			a.sources.saving = s.ID
			return a, func() tea.Msg {
				return sourceSavedMsg{id: s.ID, enabled: true, check: true, err: cfg.WriteProviderEnabled(s.ID, true)}
			}
		}
	}
	before := c.filter.Value()
	var cmd tea.Cmd
	c.filter, cmd = c.filter.Update(msg)
	if c.filter.Value() != before {
		c.win.home()
	}
	return a, cmd
}

func (a *App) viewSourceCatalog() string {
	c := a.sources.catalog
	c.filter.Width = max(1, a.contentWidth()-8)
	list := a.catalogSources()
	lines := []string{styleTitle.Render("Add a search source"), styleDim.Render("Add sources one at a time; esc finishes. Checks run in the background."), "", c.filter.View(), ""}
	lines = append(lines, renderWindow(&c.win, len(list)+1, a.catalogRows(), a.contentWidth(), func(i int, selected bool) string {
		if i == len(list) {
			return "Custom RSS / Torznab…     advanced setup"
		}
		s := list[i]
		label := padRight(s.Label, 22)
		if a.contentWidth() < 50 {
			label = s.Label
		}
		return label + " " + styleDim.Render("no account or setup required")
	}))
	detail := "Advanced: connect your own indexer manager or RSS search feed."
	if len(list) == 0 {
		detail = "No matching built-ins. Clear the filter or connect a custom feed."
	}
	if c.win.cursor < len(list) {
		detail = sources.Description(list[c.win.cursor])
	}
	if a.sources.saving != "" {
		detail = "Adding source…"
	}
	lines = append(lines, "", styleDim.Render(detail))
	return a.chrome("sources", strings.Join(lines, "\n"), hints(hint("↑↓", "choose"), hint("enter", "add & check"), hint("esc", "done")))
}
