package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// homeDest is a destination on the front-page menu.
type homeDest struct {
	name   string
	screen screen
}

var homeMenu = []homeDest{
	{"linux isos", screenISOs},
	{"downloads", screenDownloads},
}

type searchModel struct {
	input textinput.Model
	menu  int // highlighted front-page destination
}

func newSearchModel() searchModel {
	ti := textinput.New()
	ti.Placeholder = "search movies, shows, anime, software…"
	ti.CharLimit = 200
	ti.Width = 50
	ti.Prompt = "❯ "
	ti.PromptStyle = styleBrand
	ti.TextStyle = styleFg
	ti.PlaceholderStyle = styleFaint
	ti.Cursor.Style = styleBrand
	return searchModel{input: ti}
}

func (a *App) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		a.search.input, cmd = a.search.input.Update(msg)
		return a, cmd
	}

	// The field stays focused for typing; ↑↓ move the destination menu, and
	// enter searches (with a query) or opens the highlighted destination.
	switch key.String() {
	case "enter":
		if query := strings.TrimSpace(a.search.input.Value()); query != "" {
			return a, a.startSearch(query)
		}
		a.screen = homeMenu[a.search.menu].screen
		return a, nil
	case "up":
		a.search.menu = max(0, a.search.menu-1)
		return a, nil
	case "down":
		a.search.menu = min(len(homeMenu)-1, a.search.menu+1)
		return a, nil
	case "esc":
		if a.search.input.Value() != "" {
			a.search.input.SetValue("")
			return a, nil
		}
		if len(a.results.rows) > 0 {
			a.screen = screenResults
		}
		return a, nil
	}
	var cmd tea.Cmd
	a.search.input, cmd = a.search.input.Update(msg)
	return a, cmd
}

// startSearch cancels any in-flight search and fans a new one out.
func (a *App) startSearch(query string) tea.Cmd {
	if a.results.cancel != nil {
		a.results.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh, statusCh := a.agg.Search(ctx, query)

	a.results = newResultsModel(a.cfg.Ranking)
	a.results.query = query
	a.results.cancel = cancel
	a.results.resultCh = resultCh
	a.results.statusCh = statusCh
	a.results.searching = true
	a.screen = screenResults

	return tea.Batch(waitForResult(resultCh), waitForStatus(statusCh))
}

// viewSearch is the front page: a centered hero (wordmark, tagline, search
// field, and a small destination menu) above a pinned status bar.
func (a *App) viewSearch() string {
	tw, th := a.termWidth(), a.termHeight()

	fieldW := min(52, a.contentWidth())
	a.search.input.Width = fieldW - 6
	field := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBrand).
		Padding(0, 1).
		Width(fieldW - 4).
		Render(a.search.input.View())

	cue := styleFaint.Render("press enter to search")
	if strings.TrimSpace(a.search.input.Value()) == "" {
		cue = styleFaint.Render("type to search  ·  ↑↓ then enter to open")
	}

	hero := lipgloss.JoinVertical(lipgloss.Center,
		renderCat(smallCat(moodHappy), moodHappy),
		"",
		renderLogo(),
		"",
		styleDim.Render("you name it, the cat fetches it"),
		"",
		field,
		cue,
		"",
		a.homeMenuView(),
	)

	// footer status bar pinned to the bottom
	left := hints(hint("enter", "open"), hint("↑↓", "menu"), hint("tab", "browse"), hint("^c", "quit"))
	if a.errText != "" {
		left = styleErr.Render(a.errText)
	}
	right := styleFaint.Render(cozyGreeting())
	bar := " " + left + strings.Repeat(" ", max(1, tw-lipgloss.Width(left)-lipgloss.Width(right)-2)) + right + " "
	footer := styleRule.Render(strings.Repeat("─", tw)) + "\n" + bar

	top := lipgloss.Place(tw, max(1, th-2), lipgloss.Center, lipgloss.Center, hero)
	return top + "\n" + footer
}

// homeMenuView renders the small destination list under the search field.
func (a *App) homeMenuView() string {
	descs := []string{"browse & grab official distro images", a.downloadsSummary()}
	rows := make([]string, len(homeMenu))
	for i, d := range homeMenu {
		name := padRight(d.name, 12)
		if i == a.search.menu {
			rows[i] = styleBrand.Render("→ ") + styleFg.Bold(true).Render(name) + " " + styleDim.Render(descs[i])
		} else {
			rows[i] = styleFaint.Render("  ") + styleDim.Render(name) + " " + styleFaint.Render(descs[i])
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (a *App) downloadsSummary() string {
	switch n := len(a.downloads.snaps); n {
	case 0:
		return "nothing downloading yet"
	case 1:
		return "1 in progress"
	default:
		return fmt.Sprintf("%d in progress", n)
	}
}
