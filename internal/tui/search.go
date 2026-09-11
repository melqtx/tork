package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/melqtx/tork/internal/intake"
)

// homeDest is a destination on the front-page menu.
type homeDest struct {
	name   string
	screen screen
}

func (a *App) homeDestinations() []homeDest {
	menu := []homeDest{{"downloads", screenDownloads}, {"linux isos", screenISOs}}
	if a.results.query != "" {
		menu = append(menu, homeDest{"back to results", screenResults})
	}
	return menu
}

// Tab moves through the controls on home; arrows work the same way.
func (a *App) cycleHomeFocus(reverse bool) tea.Cmd {
	n := len(a.homeDestinations())
	if !a.search.menuFocused {
		a.search.menu = 0
		if reverse {
			a.search.menu = n - 1
		}
		a.search.menuFocused = true
		a.search.input.Blur()
		return nil
	}
	if reverse {
		a.search.menu--
	} else {
		a.search.menu++
	}
	if a.search.menu < 0 || a.search.menu >= n {
		return a.focusSearch()
	}
	return nil
}

type searchModel struct {
	input       textinput.Model
	menu        int // highlighted front-page destination
	menuFocused bool
}

func newSearchModel() searchModel {
	ti := textinput.New()
	ti.Placeholder = "search or paste a torrent link"
	ti.CharLimit = 4096 // tracker-rich magnets and long local paths are valid inputs
	ti.Width = 50
	ti.Prompt = "❯ "
	ti.PromptStyle = styleBrand
	ti.TextStyle = styleFg
	ti.PlaceholderStyle = styleFaint
	ti.Cursor.Style = styleBrand
	return searchModel{input: ti}
}

// OpenTorrent arranges for any supported explicit CLI input to open in the
// same quiet preview flow as pasting it on the home screen.
func (a *App) OpenTorrent(raw string) error {
	target, ok, err := intake.DetectCLI(raw)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("expected a magnet link, infohash, torrent URL, or local torrent file")
	}
	return a.OpenTarget(target)

}

// OpenTarget queues a previously classified input for the first TUI frame.
func (a *App) OpenTarget(target intake.Target) error {
	a.search.input.SetValue(target.Value)
	switch target.Kind {
	case intake.Magnet, intake.InfoHash:
		a.startup = a.launchCmd(target.Value, target.Name, true)
	case intake.TorrentURL:
		a.startup = a.launchTorrentURLPreviewCmd(target.Value, target.Name)
	case intake.TorrentFile:
		a.startup = a.launchTorrentFilePreviewCmd(target.Value, target.Name)
	default:
		return fmt.Errorf("unsupported torrent input")
	}
	return nil
}

func (a *App) updateSearch(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		a.search.input, cmd = a.search.input.Update(msg)
		return a, cmd
	}

	// Only the visibly focused control handles Enter. Typing from the menu
	// returns focus to search without losing the saved query.
	switch key.String() {
	case "enter":
		if a.search.menuFocused {
			return a, a.navigate(a.homeDestinations()[a.search.menu].screen)
		}
		if query := strings.TrimSpace(a.search.input.Value()); query != "" {
			target, detected, err := intake.DetectHome(query)
			if err != nil {
				return a, a.showError(err.Error())
			}
			if detected {
				switch target.Kind {
				case intake.TorrentURL:
					return a, a.launchTorrentURLPreviewCmd(target.Value, target.Name)
				case intake.TorrentFile:
					return a, a.launchTorrentFilePreviewCmd(target.Value, target.Name)
				default:
					return a, a.launchCmd(target.Value, target.Name, true)
				}
			}
			return a, a.startSearch(query)
		}
		return a, nil
	case "up":
		if a.search.menuFocused && a.search.menu > 0 {
			a.search.menu--
			return a, nil
		}
		return a, a.focusSearch()
	case "down":
		if a.search.menuFocused {
			a.search.menu = min(len(a.homeDestinations())-1, a.search.menu+1)
		} else {
			a.search.menu = 0
			a.search.menuFocused = true
			a.search.input.Blur()
		}
		return a, nil
	case "esc":
		if a.search.menuFocused {
			return a, a.focusSearch()
		}
		a.search.input.SetValue("")
		return a, nil
	}
	if a.search.menuFocused {
		if key.Type != tea.KeyRunes && key.Type != tea.KeySpace && key.Type != tea.KeyBackspace && key.Type != tea.KeyCtrlV {
			return a, nil
		}
		focus := a.focusSearch()
		var cmd tea.Cmd
		a.search.input, cmd = a.search.input.Update(msg)
		return a, tea.Batch(focus, cmd)
	}
	var cmd tea.Cmd
	a.search.input, cmd = a.search.input.Update(msg)
	return a, cmd
}

// startSearch cancels any in-flight search and fans a new one out.
func (a *App) startSearch(query string) tea.Cmd {
	if len(a.agg.Providers()) == 0 {
		return a.showError("no search providers are enabled in config.yaml")
	}
	if a.results.cancel != nil {
		a.results.cancel()
	}
	a.cancelResolve()
	ctx, cancel := context.WithCancel(context.Background())
	resultCh, statusCh := a.agg.Search(ctx, query)
	a.searchSeq++

	prefs := a.results.pickPrefs
	a.results = newResultsModel(a.cfg.Ranking)
	a.results.curated = true
	a.results.pickPrefs = prefs
	a.results.searchID = a.searchSeq
	a.results.query = query
	a.results.cancel = cancel
	a.results.resultCh = resultCh
	a.results.statusCh = statusCh
	a.results.searching = true
	a.screen = screenResults

	return tea.Batch(waitForResult(a.searchSeq, resultCh), waitForStatus(a.searchSeq, statusCh))
}

// viewSearch is the front page: a centered hero (wordmark, tagline, search
// field, and a small destination menu) above a pinned status bar.
func (a *App) viewSearch() string {
	th := a.termHeight()

	fieldW := max(8, min(52, a.contentWidth()))
	a.search.input.Width = max(1, fieldW-6)
	border := colBrand
	if a.search.menuFocused {
		border = colBorder
	}
	field := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(max(1, fieldW-4)).
		Render(a.search.input.View())

	var hero string
	if th < 25 || a.contentWidth() < 40 {
		hero = lipgloss.JoinVertical(lipgloss.Center,
			styleBrand.Render("tork  /ᐠ｡ꞈ｡ᐟ\\"),
			field,
			"",
			a.homeMenuView(),
		)
	} else {
		hero = lipgloss.JoinVertical(lipgloss.Center,
			renderCat(smallCat(moodHappy), moodHappy),
			"",
			renderLogo(),
			"",
			styleDim.Render("you name it, the cat fetches it"),
			"",
			field,
			"",
			a.homeMenuView(),
		)
	}
	hero = fitBlockWidth(hero, a.contentWidth())
	body := lipgloss.Place(a.contentWidth(), a.bodyHeight(), lipgloss.Center, lipgloss.Center, hero)
	return a.chrome("home", body, a.keyStrip(a.helpBudget(a.contentWidth())))
}

// homeMenuView renders the small destination list under the search field.
func (a *App) homeMenuView() string {
	resultDesc := "your latest search"
	if a.results.query != "" {
		resultDesc = a.results.query
	}
	descs := []string{a.downloadsSummary(), "official images", resultDesc}
	menu := a.homeDestinations()
	rows := make([]string, len(menu))
	for i, d := range menu {
		name := padRight(d.name, 16)
		if a.search.menuFocused && i == a.search.menu {
			rows[i] = styleBrand.Render("→ ") + styleFg.Bold(true).Render(name) + " " + styleDim.Render(descs[i])
		} else {
			rows[i] = styleFaint.Render("  ") + styleDim.Render(name) + " " + styleFaint.Render(descs[i])
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (a *App) downloadsSummary() string {
	switch n := len(a.downloadItems()); n {
	case 0:
		return "nothing downloading yet"
	case 1:
		return "1 saved download"
	default:
		return fmt.Sprintf("%d saved downloads", n)
	}
}
