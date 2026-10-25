package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
)

type resultStatusProvider string

func (p resultStatusProvider) Name() string { return string(p) }
func (resultStatusProvider) Search(context.Context, string, chan<- provider.Result) error {
	return nil
}

func TestResultSourcePanelKeepsStreamingAndReleaseSelection(t *testing.T) {
	a := picksApp()
	addPick(a, 1, "Dune 2021 1080p WEB-DL", 24, 3<<30)
	addPick(a, 2, "Dune 2021 2160p BluRay", 12, 8<<30)
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	selected := a.results.pickKey
	var fleet []provider.Provider
	for i := 0; i < 20; i++ {
		fleet = append(fleet, resultStatusProvider(fmt.Sprintf("source-%02d", i)))
	}
	a.agg = aggregator.New(fleet, 0, 0)
	r := &a.results
	r.searching = true
	r.status["source-00"] = aggregator.StatusEvent{State: aggregator.StateDone, Count: 24}
	r.status["source-01"] = aggregator.StatusEvent{State: aggregator.StateDone}
	r.status["source-02"] = aggregator.StatusEvent{State: aggregator.StateFailed, Count: 3, Err: provider.ErrFeedUnsupported}
	r.status["source-03"] = aggregator.StatusEvent{State: aggregator.StateFailed, Err: provider.ErrBlocked, RetryAt: time.Now().Add(10 * time.Minute)}
	r.status["source-04"] = aggregator.StatusEvent{State: aggregator.StateFailed, Err: fmt.Errorf("%w https://user:secret@example.invalid/api?apikey=secret", provider.ErrFeedAuth)}
	for _, want := range []string{"1 with results", "1 failed", "1 paused", "1 partial", "15 searching", "1 empty", "s details"} {
		if !strings.Contains(a.resultSourceSummary(118), want) {
			t.Errorf("source summary missing %q", want)
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	view := a.View()
	for _, want := range []string{"24 results", "no matches", "3 results · partial", "blocked · retry in 10m", "auth failed", "searching…"} {
		if !strings.Contains(view, want) {
			t.Errorf("source panel missing %q", want)
		}
	}
	if strings.Contains(view, "secret") || strings.Contains(view, "example.invalid") {
		t.Fatal("source error leaked a credential or URL")
	}
	// Inspecting a search must not launch a preview/download or copy a magnet.
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune("D")}, {Type: tea.KeyRunes, Runes: []rune("Y")}} {
		if _, cmd := a.Update(key); cmd != nil {
			t.Fatal("source panel dispatched a release action")
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if r.sourceWin.cursor != 19 || !strings.Contains(a.View(), "source-19") {
		t.Fatal("last source is not reachable")
	}
	a.Update(statusMsg{searchID: r.searchID, ev: aggregator.StatusEvent{Provider: "source-19", State: aggregator.StateDone, Count: 7}})
	if !strings.Contains(a.View(), "7 results") {
		t.Fatal("source panel stopped processing search updates")
	}
	a.Update(resultMsg{searchID: r.searchID, r: provider.Result{Title: "Dune 2021 1080p WEB-DL x265", Provider: "source-19", Seeders: 10000, Magnet: provider.BuildMagnet(fmt.Sprintf("%040x", 3), "Dune", nil)}})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if r.sourcePanel || a.screen != screenResults || r.pickKey != selected || len(r.rows) != 3 {
		t.Fatal("closing source panel lost results or selected release")
	}
}

func TestResultPanelsFitAndScrollOnSmallTerminals(t *testing.T) {
	for _, width := range []int{20, 40, 80, 120, 180} {
		for _, height := range []int{10, 14, 20, 40} {
			a := picksApp()
			a.width, a.height = width, height
			for i := 1; i <= 10; i++ {
				addPick(a, i, fmt.Sprintf("Release-%02d 1080p WEB-DL", i), i*50, 3<<30)
			}
			a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
			if !strings.Contains(a.View(), "Show 7 more") {
				t.Fatalf("%dx%d scrolled release action out of view", width, height)
			}
			for _, sourcePanel := range []bool{false, true} {
				a.results.sourcePanel = sourcePanel
				a.agg = aggregator.New([]provider.Provider{resultStatusProvider("日本語 source")}, 0, 0)
				view := a.View()
				assertRenderFits(t, view, width)
				if len(strings.Split(view, "\n")) != height {
					t.Fatalf("%dx%d panel overflowed", width, height)
				}
			}
		}
	}
}

func TestSearchSourceOutcomeDistinguishesMissingAndEmpty(t *testing.T) {
	if got := searchSourceOutcome(aggregator.StatusEvent{}, false, false); got != "not searched" {
		t.Fatal(got)
	}
	if got := searchSourceOutcome(aggregator.StatusEvent{}, false, true); got != "searching…" {
		t.Fatal(got)
	}
	if got := searchSourceOutcome(aggregator.StatusEvent{State: aggregator.StateFailed, Err: errors.New("secret")}, true, false); got != "unavailable" {
		t.Fatal(got)
	}
}
