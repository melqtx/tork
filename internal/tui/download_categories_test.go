package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestDownloadCategoryFilterIncludesLiveAndSavedEntries(t *testing.T) {
	cfg := config.Default(t.TempDir())
	st := &state.State{Entries: []state.Entry{
		{Magnet: "movie", Category: "movies", Name: "Movie"},
		{Magnet: "tv", Category: "tv", Name: "Series"},
		{Magnet: "unknown", Name: "Unknown"},
	}}
	a := New(cfg, nil, nil, st, nil)
	a.screen, a.width, a.height = screenDownloads, 100, 30
	a.downloads.snaps = []engine.Snapshot{{Magnet: "movie", State: engine.StateDownloading}}
	for _, category := range []string{"movies", "", "\x00"} {
		a.openDownloadCategoryFilter()
		for i, option := range a.downloads.categoryMenu.options {
			if option.ID == category {
				a.downloads.categoryMenu.win.cursor = i
			}
		}
		a.updateDownloadCategoryFilter(tea.KeyMsg{Type: tea.KeyEnter})
		items := a.downloadItems()
		want := 1
		if category == "\x00" {
			want = 3
		}
		if len(items) != want || a.downloads.metrics.Total != want {
			t.Fatalf("filter %q returned %d items, want %d", category, len(items), want)
		}
		if a.downloads.allMetrics.Active != 1 || a.activityChip() == "" {
			t.Fatal("category filter hid the active transfer indicator")
		}
		if category != "\x00" && items[0].Category != category {
			t.Fatalf("filter %q showed category %q", category, items[0].Category)
		}
		assertRenderFits(t, a.viewDownloads(), a.width)
	}
	if len(st.Entries) != 3 || st.Entries[0].Category != "movies" {
		t.Fatal("filter changed saved downloads")
	}
}
