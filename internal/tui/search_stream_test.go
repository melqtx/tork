package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

func streamResult(hash, title string, seeders int) provider.Result {
	return provider.Result{
		Title:    title,
		Provider: "test",
		Seeders:  seeders,
		Magnet:   provider.BuildMagnet(hash, title, nil),
	}
}

func TestSearchStreamKeepsProgressingOffResultsScreen(t *testing.T) {
	resultCh := make(chan provider.Result)
	r := newResultsModel(rank.DefaultWeights())
	r.searchID = 7
	r.query = "linux"
	r.resultCh = resultCh
	a := &App{screen: screenDownloads, results: r}

	_, cmd := a.Update(resultMsg{
		searchID: 7,
		r:        streamResult(rowHash, "Linux 1080p", 20),
	})

	if a.screen != screenDownloads {
		t.Fatalf("background result changed screen to %v", a.screen)
	}
	if len(a.results.rows) != 1 {
		t.Fatalf("background result produced %d rows, want 1", len(a.results.rows))
	}
	if cmd == nil {
		t.Fatal("background result did not re-arm the result stream")
	}
}

func TestStaleSearchMessageCannotEnterCurrentResults(t *testing.T) {
	r := newResultsModel(rank.DefaultWeights())
	r.searchID = 2
	a := &App{screen: screenResults, results: r}

	_, cmd := a.Update(resultMsg{
		searchID: 1,
		r:        streamResult(rowHash, "stale result", 999),
	})

	if len(a.results.rows) != 0 {
		t.Fatalf("stale result entered current search: %+v", a.results.rows)
	}
	if cmd != nil {
		t.Fatal("stale result unexpectedly re-armed the current stream")
	}
}

func TestLeavingResultsCancelsAndInvalidatesResolve(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newResultsModel(rank.DefaultWeights())
	r.searchID = 4
	r.resolveID = 2
	r.resolving = true
	r.resolveCancel = cancel
	a := &App{screen: screenResults, results: r}

	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Fatal("ctrl+d with no persisted state unexpectedly started work")
	}
	if a.screen != screenDownloads || a.results.resolving || a.results.resolveID != 3 {
		t.Fatalf("screen=%v resolving=%v resolveID=%d", a.screen, a.results.resolving, a.results.resolveID)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("leaving results did not cancel the resolver context")
	}

	_, cmd = a.Update(magnetResolvedMsg{searchID: 4, resolveID: 2, magnet: "stale"})
	if cmd != nil || a.screen != screenDownloads {
		t.Fatal("stale resolve completion acted after leaving results")
	}
}

func TestResolveIDsCannotCollideAcrossSearches(t *testing.T) {
	r := newResultsModel(rank.DefaultWeights())
	r.searchID = 8
	r.resolveID = 1
	r.resolving = true
	a := &App{screen: screenResults, results: r}

	_, cmd := a.Update(magnetResolvedMsg{
		searchID:  7,
		resolveID: 1,
		magnet:    "magnet:?xt=urn:btih:stale",
	})
	if cmd != nil || !a.results.resolving {
		t.Fatal("a resolver completion from an older search matched the current resolve ID")
	}
}

func TestOlderErrorTimerCannotClearNewError(t *testing.T) {
	a := &App{}
	a.showError("first")
	firstGen := a.errGen
	a.showError("second")

	a.Update(clearErrMsg{gen: firstGen})
	if a.errText != "second" {
		t.Fatalf("old timer cleared newer error: %q", a.errText)
	}
	a.Update(clearErrMsg{gen: a.errGen})
	if a.errText != "" {
		t.Fatalf("current timer left error visible: %q", a.errText)
	}
}

func TestPinnedSelectionSurvivesStreamedSortedInsert(t *testing.T) {
	r := newTestResults()
	r.sort = sortSeeders
	r.insertRow(streamResult(rowHash, "first", 100))
	r.insertRow(streamResult(otherHash, "chosen", 50))
	r.refreshFilter()
	r.win.cursor = 1
	r.selectionPinned = true
	r.syncFlatSelection()
	want := r.selectedKey

	const newHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	r.insertRow(streamResult(newHash, "new best", 500))
	r.refreshFilter()

	if r.selectedKey != want {
		t.Fatalf("selected identity changed from %q to %q", want, r.selectedKey)
	}
	if r.win.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 after a better row arrived above it", r.win.cursor)
	}
	got := r.rows[r.visible[r.win.cursor]].res.Title
	if got != "chosen" {
		t.Fatalf("cursor now points at %q, want chosen", got)
	}
}

func TestUnpinnedSelectionFollowsBestLiveResult(t *testing.T) {
	r := newTestResults()
	r.sort = sortSeeders
	r.insertRow(streamResult(rowHash, "first", 100))
	r.refreshFilter()

	r.insertRow(streamResult(otherHash, "new best", 500))
	r.refreshFilter()

	if r.win.cursor != 0 {
		t.Fatalf("cursor = %d, want live best at 0", r.win.cursor)
	}
	if got := r.rows[r.visible[0]].res.Title; got != "new best" {
		t.Fatalf("top live result = %q, want new best", got)
	}
}

func TestFlatLiveResultsIncludeDecisionPanel(t *testing.T) {
	r := newTestResults()
	r.searching = true
	r.insertRow(streamResult(rowHash, "Linux Release 1080p BluRay x265", 100))
	r.refreshFilter()
	a := &App{width: 100, height: 30, screen: screenResults, results: r, agg: aggregator.New(nil, 0, 0)}

	view := a.viewResults()
	for _, want := range []string{"· live", "score", "magnet ready", "looks clean"} {
		if !strings.Contains(view, want) {
			t.Errorf("live result view omits %q", want)
		}
	}
}
