package tui

import (
	"path/filepath"
	"testing"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestMoveMetadataOnlyDownload(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DownloadDir = t.TempDir()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	magnet := "magnet:?xt=urn:btih:0123456789012345678901234567890123456789"
	h, err := eng.Add(magnet, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{Entries: []state.Entry{{Magnet: magnet, DownloadDir: cfg.DownloadDir}}}
	a := New(cfg, eng, nil, st, nil)
	snap, _ := eng.Snapshot(h)
	it := itemFromSnapshot(snap, 0, true)
	if cmd := a.openDownloadCategoryMove(it); cmd != nil {
		t.Fatal("metadata-only download was refused")
	}
	target := filepath.Join(cfg.DownloadDir, "movies")
	if err := a.moveDownload(it, target); err != nil {
		t.Fatal(err)
	}
	snap, ok := eng.Snapshot(h)
	if !ok || snap.State != engine.StateFetchingMeta || snap.DownloadDir != target || st.Entries[0].DownloadDir != target {
		t.Fatalf("metadata download did not follow its new destination: %+v", snap)
	}
	destination, err := cfg.ResolveCategoryDestination("anime")
	if err != nil {
		t.Fatal(err)
	}
	a.downloads.snaps = eng.Snapshots()
	a.refreshDownloadItems()
	cmd := a.startCategoryMove(a.downloadItems()[0], destination, "manual")
	a.Update(cmd())
	snap, ok = eng.Snapshot(h)
	if !ok || snap.State != engine.StateFetchingMeta || snap.DownloadDir != destination.DownloadDir {
		t.Fatalf("background move lost metadata discovery: %+v", snap)
	}
	loaded, err := state.Load(cfg.StatePath())
	if err != nil || loaded.Entries[0].Category != "anime" || loaded.Entries[0].CategorySource != "manual" {
		t.Fatalf("metadata move was not saved: %+v, %v", loaded, err)
	}

}
