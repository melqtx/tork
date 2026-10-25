package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/muesli/termenv"
)

func browseTestApp() *App {
	a := navigationApp()
	a.screen = screenResults
	a.results.browser = true
	a.results.query = "linux"
	for i, category := range []string{"Games", "Movies", "TV", "Anime", "Apps", "Music", "Books", ""} {
		a.results.insertRow(provider.Result{Title: fmt.Sprintf("Linux %s release", category), Category: category, Provider: "knaben", Seeders: 20 - i, SizeBytes: 2 << 30, Magnet: provider.BuildMagnet(fmt.Sprintf("%040x", i+1), "linux", nil)})
	}
	a.results.refreshFilter()
	return a
}
func TestBrowseFiltersWithoutLosingSearch(t *testing.T) {
	a := browseTestApp()
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	if a.screen != screenResults || !a.results.navFocused || len(a.results.visible) != 1 || browseCategory(a.results.rows[a.results.visible[0]]) != "Games" {
		t.Fatal("category navigation failed")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.results.navFocused || a.results.query != "linux" {
		t.Fatal("category selection lost query")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !a.results.editSearch || !a.typing() {
		t.Fatal("search not focused")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H? ")})
	if a.showHelp || a.screen != screenResults {
		t.Fatal("text triggered shortcuts")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.results.editSearch || a.results.query != "linux" || len(a.results.visible) != 1 {
		t.Fatal("cancel changed search or filters")
	}
}
func TestBrowseFramesFitEveryFocusAndSize(t *testing.T) {
	a := browseTestApp()
	for _, width := range []int{8, 20, 40, 80, 100, 144} {
		for _, height := range []int{10, 14, 24, 40} {
			a.width, a.height = width, height
			for _, focus := range []string{"list", "nav", "search", "filter"} {
				a.results.navFocused = focus == "nav"
				a.results.navCursor = len(browseCategories) + 2
				a.results.editSearch = focus == "search"
				a.results.filtering = focus == "filter"
				view := a.View()
				assertRenderFits(t, view, width)
				if len(strings.Split(view, "\n")) != height {
					t.Fatalf("wrong height %dx%d %s", width, height, focus)
				}
				if focus == "nav" && width >= 40 && !strings.Contains(view, "Home") {
					t.Fatal("selected navigation item clipped")
				}
			}
		}
	}
}
func TestBrowseSelectionSurvivesIncomingResults(t *testing.T) {
	a := browseTestApp()
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	key := a.results.selectedKey
	a.results.insertRow(provider.Result{Title: "linux", Provider: "nyaa", Seeders: 900, Magnet: provider.BuildMagnet(fmt.Sprintf("%040x", 99), "linux", nil)})
	a.results.refreshFilter()
	if rowIdentity(a.results.rows[a.results.visible[a.results.win.cursor]]) != key {
		t.Fatal("stream moved selected result")
	}
}
func TestBrowsePreviewFixture(t *testing.T) {
	path := os.Getenv("TORK_BROWSE_PREVIEW")
	if path == "" {
		t.Skip("optional visual fixture")
	}
	a := browseTestApp()
	a.width, a.height = 120, 28
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	dark := lipgloss.HasDarkBackground()
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetHasDarkBackground(dark)
	a.agg = aggregator.New([]provider.Provider{provider.NewKnaben(nil, "")}, 0, 0)
	a.results.status["knaben"] = aggregator.StatusEvent{Provider: "knaben", State: aggregator.StateDone, Count: 8}
	if err := os.WriteFile(path, []byte(a.View()), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestBrowseSeedFilterAndReset(t *testing.T) {
	a := browseTestApp()
	a.results.rows[0].res.Seeders = 0
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if len(a.results.visible) != 7 {
		t.Fatal("seed filter did not exclude zero seeders")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if len(a.results.visible) != 8 || a.results.hideDead {
		t.Fatal("reset did not restore results")
	}
}
func TestBrowsePreferenceChangesReorderTheList(t *testing.T) {
	a := browseTestApp()
	for i := range a.results.rows {
		a.results.rows[i].res.Title = "linux"
		a.results.rows[i].res.SizeBytes = int64(8-i) << 30
	}
	a.results.pickPrefs.preference = 1
	a.results.refreshFilter()
	if a.results.rows[a.results.visible[0]].res.SizeBytes != 1<<30 {
		t.Fatal("smaller-files preference ignored in browse")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	if a.results.rows[a.results.visible[0]].res.Seeders != 20 {
		t.Fatal("explicit seed sort did not override preference")
	}
}
