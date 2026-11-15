package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestCategoryMovePreservesPausedAndCompletedDownloads(t *testing.T) {
	cases := []struct {
		name   string
		paused bool
		done   bool
	}{
		{name: "paused", paused: true},
		{name: "completed", done: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default(t.TempDir())
			cfg.DownloadDir = t.TempDir()
			cfg.Organization.Folders = map[string]string{"Courses": "Learning"}
			old := filepath.Join(cfg.DownloadDir, "payload.dat")
			if err := os.WriteFile(old, []byte("keep these bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			st := &state.State{Entries: []state.Entry{{
				Magnet: "saved", Name: "payload.dat", DownloadDir: cfg.DownloadDir, DataPath: old,
				Paused: tc.paused, Done: tc.done, Seed: state.Bool(false), Excluded: []int{1},
			}}}
			a := New(cfg, &engine.Engine{}, nil, st, nil)
			a.width, a.height = 100, 30
			a.openDownloadCategoryMove(a.downloadItems()[0])
			for i, option := range a.downloads.categoryMove.menu.options {
				if option.ID == "Courses" {
					a.downloads.categoryMove.menu.win.cursor = i
				}
			}
			a.updateDownloadCategoryMove(tea.KeyMsg{Type: tea.KeyEnter})
			if _, err := os.Stat(old); err != nil {
				t.Fatal("category selection moved files before confirmation")
			}
			assertRenderFits(t, a.viewDownloadCategoryMove(a.downloads.categoryMove), a.width)
			a.updateDownloadCategoryMove(tea.KeyMsg{Type: tea.KeyEnter})
			want := filepath.Join(cfg.DownloadDir, "Learning", "payload.dat")
			data, err := os.ReadFile(want)
			if err != nil || string(data) != "keep these bytes" {
				t.Fatalf("move lost the payload: %v", err)
			}
			e := st.Entries[0]
			if e.Category != "Courses" || e.DataPath != want || e.Paused != tc.paused || e.Done != tc.done {
				t.Fatalf("move lost saved settings: %+v", e)
			}
			if len(e.Excluded) != 1 || *e.Seed {
				t.Fatal("move changed file exclusions or seeding policy")
			}
		})
	}
}
