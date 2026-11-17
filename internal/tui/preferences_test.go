package tui

import (
	"encoding/json"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestPreferencesSurviveAFullRestart(t *testing.T) {
	for _, category := range []string{"movies", ""} {
		cfg := config.Default(t.TempDir())
		a := New(cfg, &engine.Engine{}, nil, &state.State{}, nil)
		a.screen, a.results.browser = screenResults, true
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
		a.results.navCursor = 2
		a.updateBrowseNav(tea.KeyMsg{Type: tea.KeyEnter})
		a.openDownloadCategoryFilter()
		for i, option := range a.downloads.categoryMenu.options {
			if option.ID == category {
				a.downloads.categoryMenu.win.cursor = i
			}
		}
		a.updateDownloadCategoryFilter(tea.KeyMsg{Type: tea.KeyEnter})
		loaded, err := state.Load(cfg.StatePath())
		if err != nil {
			t.Fatal(err)
		}
		restarted := New(cfg, &engine.Engine{}, nil, loaded, nil)
		if restarted.results.sort != sortSeeders || restarted.results.category != 2 || !restarted.results.hideDead {
			t.Fatal("restart lost search preferences")
		}
		if restarted.downloads.categoryFilter == nil || *restarted.downloads.categoryFilter != category {
			t.Fatalf("restart lost category filter %q", category)
		}
		restarted.screen, restarted.results.browser = screenResults, true
		restarted.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		loaded, err = state.Load(cfg.StatePath())
		if err != nil {
			t.Fatal(err)
		}
		reset := New(cfg, &engine.Engine{}, nil, loaded, nil)
		if reset.results.hideDead || reset.results.category != 2 || reset.results.navCursor != 2 {
			t.Fatal("reset or category navigation was not restored")
		}
	}
}

func TestInvalidPreferencesKeepTheDownloadList(t *testing.T) {
	for _, raw := range []string{`{"search_sort":"unknown","search_category":"removed","download_category":"removed"}`, `{"search_sort":7}`} {
		cfg := config.Default(t.TempDir())
		st := &state.State{Entries: []state.Entry{{Magnet: "saved", Name: "keep me", Paused: true}}, Preferences: json.RawMessage(raw)}
		if err := st.Save(cfg.StatePath()); err != nil {
			t.Fatal(err)
		}
		loaded, err := state.Load(cfg.StatePath())
		if err != nil {
			t.Fatal(err)
		}
		a := New(cfg, &engine.Engine{}, nil, loaded, nil)
		if a.results.sort != sortScore || a.results.category != 0 || a.downloads.categoryFilter != nil || len(a.downloadItems()) != 1 {
			t.Fatal("invalid preferences hid or removed saved downloads")
		}
	}
}
