package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

func picksApp() *App {
	a := navigationApp()
	a.screen = screenResults
	a.results.curated = true
	a.results.query = "Dune 2021"
	return a
}
func addPick(a *App, n int, title string, seeds int, size int64) {
	res := provider.Result{Title: title, Provider: "test", Seeders: seeds, SizeBytes: size, Magnet: provider.BuildMagnet(fmt.Sprintf("%040x", n), title, nil)}
	a.results.insertRow(res)
	a.results.refreshFilter()
}
func pickTitles(r *resultsModel) []string {
	var out []string
	for _, it := range r.picks {
		if it.row >= 0 {
			out = append(out, r.rows[it.row].res.Title)
		}
	}
	return out
}
func TestPicksRankExactTitleYearAndEpisodeBeforeSwarm(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 1984 1080p BluRay", 10000, 3<<30)
	addPick(a, 2, "Dune 2021 1080p WEB-DL", 20, 3<<30)
	addPick(a, 3, "Children of Dune 2021 1080p", 50000, 3<<30)
	if got := pickTitles(&a.results)[0]; got != "Dune 2021 1080p WEB-DL" {
		t.Fatalf("wrong first pick: %s", got)
	}
	a = picksApp()
	a.results.query = "Example S02E03"
	addPick(a, 1, "Example S02E04 1080p", 5000, 1<<30)
	addPick(a, 2, "Example S02E03 1080p", 5, 1<<30)
	if got := pickTitles(&a.results)[0]; !strings.Contains(got, "E03") {
		t.Fatalf("wrong episode: %s", got)
	}
}
func TestPicksLimitEachResolutionAndExpandWithoutDownloading(t *testing.T) {
	a := picksApp()
	for i := 1; i <= 5; i++ {
		addPick(a, i, fmt.Sprintf("Dune 2021 1080p WEB-DL release%d", i), 50+i, int64(i)<<30)
	}
	addPick(a, 6, "Dune 2021 2160p WEB-DL", 100, 12<<30)
	addPick(a, 7, "Dune audiobook", 10, 1<<30)
	if len(pickTitles(&a.results)) != 5 {
		t.Fatal("expected 3 HD, 1 UHD and 1 unspecified")
	}
	for i, it := range a.results.picks {
		if it.row == -2 {
			a.results.pickWin.cursor = i
			a.results.pickKey = it.key
			a.results.pickPinned = true
			break
		}
	}
	_, cmd := a.updatePickKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || len(pickTitles(&a.results)) != 7 {
		t.Fatal("expanding should reveal all without starting a download")
	}
	a.updatePickKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if len(pickTitles(&a.results)) != 5 {
		t.Fatal("collapse did not restore top three")
	}
	if !strings.Contains(a.View(), "1080p") || !strings.Contains(a.View(), "2160p") {
		t.Fatal("resolution headings missing")
	}
}
func TestPickSelectionSurvivesStreaming(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 2021 1080p WEB-DL", 20, 3<<30)
	addPick(a, 2, "Dune 2021 1080p BluRay", 10, 3<<30)
	a.updatePickKeys(tea.KeyMsg{Type: tea.KeyDown})
	key := a.results.pickKey
	addPick(a, 3, "Dune 2021 1080p REMUX", 500, 20<<30)
	addPick(a, 4, "Dune 2021 1080p WEB-DL newer", 900, 3<<30)
	if a.results.pickKey != key {
		t.Fatal("streaming changed selected release")
	}
	if a.results.picks[a.results.pickWin.cursor].key != key {
		t.Fatal("cursor no longer represents selected release")
	}
}
func TestPickFiltersMultipleResolutionsApplyCancelReset(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 2021 720p", 10, 1<<30)
	addPick(a, 2, "Dune 2021 1080p", 10, 3<<30)
	addPick(a, 3, "Dune 2021 2160p", 10, 10<<30)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	a.Update(tea.KeyMsg{Type: tea.KeySpace})
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	a.Update(tea.KeyMsg{Type: tea.KeySpace})
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if a.screen != screenResults || !a.results.pickPanel {
		t.Fatal("Tab escaped panel")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(a.results.visible) != 2 {
		t.Fatalf("multi-resolution should OR choices; got %d", len(a.results.visible))
	}
	a.openPickFilters()
	a.results.pickDraft.size = 1
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if len(a.results.visible) != 2 || a.results.pickPrefs.size != 0 {
		t.Fatal("cancel changed applied filters")
	}
	a.openPickFilters()
	a.results.panelWin.cursor = 8
	a.Update(tea.KeyMsg{Type: tea.KeySpace})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(a.results.visible) != 3 {
		t.Fatal("reset failed")
	}
}
func TestPickPreferencesAndUnknownSizes(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 2021 1080p WEB-DL", 50, 2<<30)
	addPick(a, 2, "Dune 2021 1080p REMUX", 50, 12<<30)
	addPick(a, 3, "Dune 2021 1080p BluRay", 50, 0)
	a.results.pickPrefs.preference = 1
	a.results.refreshFilter()
	if !strings.Contains(pickTitles(&a.results)[0], "WEB-DL") {
		t.Fatal("smaller mode ignored size")
	}
	a.results.pickPrefs.preference = 2
	a.results.refreshFilter()
	if !strings.Contains(pickTitles(&a.results)[0], "REMUX") {
		t.Fatal("quality mode ignored source")
	}
	a.results.pickPrefs.size = 2
	a.results.refreshFilter()
	if len(a.results.visible) != 1 {
		t.Fatal("size limit must exclude large and unknown sizes")
	}
}
func TestCommaResolutionFilterAndNegation(t *testing.T) {
	for _, s := range []string{"res:1080p,2160p", "-res:720p,480p"} {
		f := parseResultFilter(s)
		if f.err != nil {
			t.Fatal(f.err)
		}
		for _, res := range []rank.Resolution{rank.Res1080, rank.Res2160} {
			if !f.matches(scoredRow{tags: rank.Tags{Resolution: res}}) {
				t.Fatalf("%s rejected %s", s, res)
			}
		}
	}
	if parseResultFilter("res:1080p,").err == nil {
		t.Fatal("malformed list accepted")
	}
}
func TestPickViewsFitAndFirstHeadingRemainsVisible(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 2021 1080p WEB-DL", 10, 3<<30)
	for _, width := range []int{20, 40, 80, 120} {
		a.width = width
		a.height = 20
		for _, panel := range []bool{false, true} {
			a.results.pickPanel = panel
			view := a.View()
			assertRenderFits(t, view, width)
			if len(strings.Split(view, "\n")) != a.height {
				t.Fatal("wrong frame height")
			}
			if !panel && !strings.Contains(view, "1080p · 1 release") {
				t.Fatal("first group header scrolled out before user moved")
			}
		}
	}
}

func TestPicksAndFullListPreserveSelection(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 2021 1080p WEB-DL", 20, 3<<30)
	addPick(a, 2, "Dune 2021 2160p BluRay", 10, 12<<30)
	a.updatePickKeys(tea.KeyMsg{Type: tea.KeyDown})
	key := a.results.pickKey
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if a.results.curated || a.results.selectedKey != key {
		t.Fatal("full list lost selection")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	if !a.results.curated || a.results.pickKey != key {
		t.Fatal("return to picks lost selection")
	}
}

func TestPicksFollowBestUntilUserMoves(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 2021 1080p WEB-DL", 10, 3<<30)
	addPick(a, 2, "Dune 2021 1080p BluRay", 1000, 3<<30)
	it := a.results.picks[a.results.pickWin.cursor]
	if it.row < 0 || !strings.Contains(a.results.rows[it.row].res.Title, "BluRay") {
		t.Fatal("untouched selection should follow best match")
	}
}
