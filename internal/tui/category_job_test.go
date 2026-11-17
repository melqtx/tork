package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestCategoryMoveCommitsOnlyAfterBackgroundResult(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DownloadDir = t.TempDir()
	old := filepath.Join(cfg.DownloadDir, "payload.txt")
	if err := os.WriteFile(old, []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state.State{Entries: []state.Entry{{Magnet: "saved", Name: "payload.txt", Paused: true,
		DownloadDir: cfg.DownloadDir, DataPath: old, Seed: state.Bool(false)}}}
	st.Preferences = json.RawMessage(`{"search_sort":"size"}`)
	a := New(cfg, &engine.Engine{}, nil, st, nil)
	destination, err := cfg.ResolveCategoryDestination("movies")
	if err != nil {
		t.Fatal(err)
	}
	cmd := a.startCategoryMove(a.downloadItems()[0], destination, "manual")
	if !a.downloads.moving || st.Entries[0].PendingMove != nil {
		t.Fatal("background work mutated live state")
	}
	if err := a.CheckpointSession(); err == nil {
		t.Fatal("checkpoint allowed during a move")
	}
	msg := cmd().(categoryMovedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if st.Entries[0].DataPath != old {
		t.Fatal("worker mutated the UI-owned state")
	}
	raw, err := os.ReadFile(cfg.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	var journal state.State
	if err := json.Unmarshal(raw, &journal); err != nil || journal.Entries[0].PendingMove == nil {
		t.Fatalf("move has no durable recovery record: %v", err)
	}
	recovered, err := state.Load(cfg.StatePath())
	if err != nil || recovered.Entries[0].Category != "movies" || recovered.Entries[0].PendingMove != nil {
		t.Fatalf("restart between rename and UI commit failed: %+v, %v", recovered, err)
	}
	var preferences uiPreferences
	if json.Unmarshal(recovered.Preferences, &preferences) != nil || preferences.SearchSort != "size" {
		t.Fatal("move journal lost preferences")
	}
	a.Update(msg)
	want := filepath.Join(destination.DownloadDir, "payload.txt")
	if data, err := os.ReadFile(want); err != nil || string(data) != "preserve me" {
		t.Fatalf("payload changed: %v", err)
	}
	loaded, err := state.Load(cfg.StatePath())
	if err != nil || loaded.Entries[0].PendingMove != nil || loaded.Entries[0].Category != "movies" || !loaded.Entries[0].Paused {
		t.Fatalf("move did not commit: %+v, %v", loaded, err)
	}
	if json.Unmarshal(loaded.Preferences, &preferences) != nil || preferences.SearchSort != "size" {
		t.Fatal("committed move lost preferences")
	}
}
