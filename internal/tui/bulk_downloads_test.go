package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestBulkControlsRespectFilterAndCancellation(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DownloadDir = t.TempDir()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	st := &state.State{}
	for i, category := range []string{"movies", "movies", "books"} {
		magnet := fmt.Sprintf("magnet:?xt=urn:btih:%040x", i+1)
		if _, err := eng.AddWithOptions(magnet, engine.AddOptions{Seed: state.Bool(false)}); err != nil {
			t.Fatal(err)
		}
		st.Upsert(state.Entry{Magnet: magnet, Name: fmt.Sprint(i), Category: category,
			DownloadDir: cfg.DownloadDir, Seed: state.Bool(false)})
	}
	a := New(cfg, eng, nil, st, nil)
	a.screen = screenDownloads
	category := "movies"
	a.downloads.categoryFilter = &category
	a.downloads.snaps = eng.Snapshots()
	a.refreshDownloadItems()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	if len(a.downloads.bulk.items) != 2 {
		t.Fatal("bulk action included a different category")
	}
	if err := a.CheckpointSession(); err == nil {
		t.Fatal("checkpoint allowed during a bulk action")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(cmd())
	if a.downloads.bulk != nil || !st.Entries[0].Paused || st.Entries[1].Paused || st.Entries[2].Paused {
		t.Fatal("cancel did not stop after the current item")
	}
	for _, key := range []rune{'P', 'R'} {
		_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		for a.downloads.bulk != nil {
			if cmd == nil {
				t.Fatal("bulk action stopped without completing")
			}
			_, cmd = a.Update(cmd())
		}
		wantPaused := key == 'P'
		if st.Entries[0].Paused != wantPaused || st.Entries[1].Paused != wantPaused || st.Entries[2].Paused {
			t.Fatalf("%c changed the wrong transfers: %+v", key, st.Entries)
		}
	}
	loaded, err := state.Load(cfg.StatePath())
	if err != nil || loaded.Entries[0].Paused || loaded.Entries[1].Paused {
		t.Fatalf("bulk resume was not persisted: %+v, %v", loaded, err)
	}
}

func TestPausedCompletedDownloadCanResumeAfterRestart(t *testing.T) {
	cfg := config.Default(t.TempDir())
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state.State{Entries: []state.Entry{{Magnet: "saved", Name: "payload", Done: true,
		Paused: true, DataPath: path, DownloadDir: filepath.Dir(path), Seed: state.Bool(true)}}}
	a := New(cfg, &engine.Engine{}, nil, st, nil)
	it := a.downloadItems()[0]
	if it.State != engine.StatePaused || !bulkEligible(it, false) || bulkEligible(it, true) {
		t.Fatalf("paused seeding entry cannot resume: %+v", it)
	}
}
