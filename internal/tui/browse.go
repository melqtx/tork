package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var browseCategories = []string{"All", "Games", "Movies", "TV", "Anime", "Software", "Music", "Books", "Other"}

func browseCategory(row scoredRow) string {
	c := strings.ToLower(row.res.Category)
	switch {
	case strings.Contains(c, "anime"):
		return "Anime"
	case strings.Contains(c, "game") || c == "pc" || strings.Contains(c, "console"):
		return "Games"
	case strings.Contains(c, "movie") || strings.Contains(c, "film"):
		return "Movies"
	case c == "tv" || strings.Contains(c, "television") || strings.Contains(c, "tv "):
		return "TV"
	case strings.Contains(c, "app") || strings.Contains(c, "software"):
		return "Software"
	case strings.Contains(c, "music") || strings.Contains(c, "audio"):
		return "Music"
	case strings.Contains(c, "book"):
		return "Books"
	}
	return "Other"
}

func (a *App) browseWidth() int {
	w := a.contentWidth()
	if w >= 76 {
		return w - 16
	}
	return w
}
func browseReleaseLayout(width int) releaseLayout {
	l := newReleaseLayout(width)
	if l.quality > 14 {
		l.title += l.quality - 14
		l.quality = 14
	}
	return l
}
func (a *App) browseRows() int {
	if a.bodyHeight() < 12 {
		return max(1, a.bodyHeight()-5)
	}
	return max(1, a.bodyHeight()-9)
}
func (a *App) editBrowseSearch() tea.Cmd {
	a.results.editSearch = true
	a.results.navFocused = false
	a.search.input.SetValue(a.results.query)
	a.search.menuFocused = false
	return a.search.input.Focus()
}
func (a *App) updateBrowseSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "down", "tab":
			a.results.editSearch = false
			a.search.input.Blur()
			return a, nil
		case "enter":
			if strings.TrimSpace(a.search.input.Value()) == "" {
				return a, nil
			}
			a.results.editSearch = false
			a.search.input.Blur()
			return a.updateSearch(msg)
		}
	}
	var cmd tea.Cmd
	a.search.input, cmd = a.search.input.Update(msg)
	return a, cmd
}
func (a *App) updateBrowseNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	total := len(browseCategories) + 3
	switch msg.String() {
	case "q":
		return a, tea.Quit
	case "up", "k":
		r.navCursor = max(0, r.navCursor-1)
	case "down", "j":
		r.navCursor = min(total-1, r.navCursor+1)
	case "esc", "right", "tab":
		r.navFocused = false
		return a, nil
	case "/":
		return a, a.editBrowseSearch()
	case "enter":
		if r.navCursor >= len(browseCategories) {
			r.navFocused = false
			return a, a.navigate([]screen{screenDownloads, screenSources, screenSearch}[r.navCursor-len(browseCategories)])
		}
		r.navFocused = false
	}
	if r.navCursor < len(browseCategories) && r.category != r.navCursor {
		r.category = r.navCursor
		r.win.home()
		r.selectionPinned = false
		r.refreshFilter()
	}
	return a, nil
}

func (a *App) viewBrowse() string {
	r := &a.results
	w := a.browseWidth()
	a.search.input.Width = max(1, w-10)
	search := styleDim.Render("Search  ") + styleFg.Render(r.query) + styleFaint.Render("  / edit")
	if r.editSearch {
		search = styleKey.Render("Search  ") + a.search.input.View()
	}
	if r.query == "" && !r.editSearch {
		search = styleDim.Render("/ search or paste a torrent link")
	}
	summary := fmt.Sprintf("%d results · %s", len(r.visible), browseCategories[r.category])
	if r.searching {
		summary += " · searching…"
	}
	sortLabel := r.sort.String()
	if r.sort == sortScore {
		sortLabel = "relevance"
	}
	summary += " · " + sortLabel
	if r.filterIn.Value() != "" || r.pickPrefs.resolutions != 0 || r.pickPrefs.size != 0 {
		summary += " · filtered"
	}
	if r.hideDead {
		summary += " · seeded only"
	}
	body := truncate(search, w) + "\n" + rule(w) + "\n" + styleBrand.Render(summary) + "\n"
	body += a.resultSourceSummary(w) + "\n"
	body += " " + browseReleaseLayout(w-1).render("Name", "Source", "Size", "Seeders", true) + "\n"
	if len(r.visible) == 0 {
		message := "No matches. Change category or filters. / edit search"
		if r.searching {
			message = "Searching… results appear as sources respond."
		}
		body += padLines(styleDim.Render(truncate(message, w)), a.browseRows())
	} else {
		body += renderWindow(&r.win, len(r.visible), a.browseRows(), w, func(i int, selected bool) string {
			row := r.rows[r.visible[i]]
			size := "?"
			if row.res.SizeBytes > 0 {
				size = humanBytes(row.res.SizeBytes)
			}
			seeds := fmt.Sprint(max(0, row.res.Seeders))
			return browseReleaseLayout(w-1).render(readableReleaseTitle(row.res.Title), sourceTag(row.res, true), size, seeds, false)
		})
	}
	if a.bodyHeight() >= 12 {
		label := "Choose a result · enter preview"
		title, facts := "", ""
		if r.win.cursor < len(r.visible) {
			row := r.rows[r.visible[r.win.cursor]]
			label = fmt.Sprintf("Selected %d / %d", r.win.cursor+1, len(r.visible))
			title = row.res.Title
			facts = browseCategory(row) + " · " + sourceTag(row.res, true) + " · enter preview · Y copy magnet"
		}
		body += "\n" + detailRule(label, w) + "\n" + styleFg.Render(truncate(title, w)) + "\n" + styleDim.Render(truncate(facts, w))
	}
	nav := a.browseNav()
	if a.contentWidth() >= 76 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, padLines(nav, a.bodyHeight()), "  ", padLines(fitBlockWidth(body, w), a.bodyHeight()))
	} else if r.navFocused {
		body = styleTitle.Render("Browse") + "\n" + nav
	}
	help := a.keyStrip(a.helpBudget(a.contentWidth()))
	if r.navFocused {
		help = hints(hint("↑↓", "choose"), hint("enter", "open"), hint("tab", "results"))
	}
	if r.editSearch {
		help = hints(hint("enter", "search"), hint("esc", "cancel"), hint("↓", "results"))
	}
	if r.filtering {
		r.filterIn.Width = max(1, a.contentWidth()-2)
		help = r.filterIn.View()
		if r.filterErr != "" {
			help += "  " + styleErr.Render(r.filterErr)
		}
	}
	return a.chrome("tork · browse", body, help)
}
func (a *App) browseNav() string {
	r := &a.results
	labels := append(append([]string{}, browseCategories...), "Downloads", "Sources", "Home")
	if !r.navFocused {
		var lines []string
		for i, label := range labels {
			text := styleDim.Render("  " + label)
			if i == r.category {
				text = styleBrand.Render("· " + label)
			}
			lines = append(lines, padRight(text, 14))
		}
		return strings.Join(lines, "\n")
	}
	win := listWindow{cursor: r.category}
	if r.navFocused {
		win.cursor = r.navCursor
	}
	rows := max(1, a.bodyHeight()-1)
	return renderWindow(&win, len(labels), min(rows, len(labels)), 14, func(i int, selected bool) string {
		if i == r.category {
			return styleBrand.Render(labels[i])
		}
		return styleDim.Render(labels[i])
	})
}
