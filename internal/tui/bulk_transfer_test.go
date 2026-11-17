package tui

import (
	"testing"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestBulkTransferWorkerPreservesOwnerStateAndSettings(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DownloadDir = t.TempDir()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	magnet := "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01"
	hash, err := eng.AddWithOptions(magnet, engine.AddOptions{Seed: state.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Entries: []state.Entry{{Magnet: magnet, Name: "sample", Category: "movies",
		DownloadDir: cfg.DownloadDir, Excluded: []int{3}, Seed: state.Bool(false)}}}
	a := New(cfg, eng, nil, st, nil)
	for _, pause := range []bool{true, false} {
		a.downloads.snaps = eng.Snapshots()
		a.refreshDownloadItems()
		msg := a.bulkTransferCmd(a.downloadItems()[0], pause)().(bulkTransferMsg)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		if st.Entries[0].Paused == pause {
			t.Fatal("worker mutated event-loop state")
		}
		if msg.entry.Paused != pause || msg.entry.Category != "movies" || *msg.entry.Seed || len(msg.entry.Excluded) != 1 {
			t.Fatalf("worker lost download settings: %+v", msg.entry)
		}
		snap, ok := eng.Snapshot(hash)
		if !ok || (snap.State == engine.StatePaused) != pause {
			t.Fatalf("transfer state did not change: %+v", snap)
		}
		st.Upsert(msg.entry)
	}
}
