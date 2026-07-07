package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestDownloadItemsShowsMissingCompletedData(t *testing.T) {
	cfg := &config.Config{DownloadDir: t.TempDir(), SeedAfterComplete: true}
	st := &state.State{}
	st.Upsert(state.Entry{
		Magnet:      "magnet:?xt=urn:btih:abc",
		Name:        "gone",
		AddedAt:     time.Now().UTC(),
		Done:        true,
		DownloadDir: cfg.DownloadDir,
		DataPath:    filepath.Join(cfg.DownloadDir, "gone"),
	})
	app := &App{cfg: cfg, st: st}

	items := app.downloadItems()
	if len(items) != 1 {
		t.Fatalf("downloadItems returned %d items, want 1", len(items))
	}
	if items[0].State != engine.StateMissing {
		t.Fatalf("state = %s, want missing data", items[0].State)
	}
}

func TestDownloadItemsShowsRelinkNeededData(t *testing.T) {
	cfg := &config.Config{DownloadDir: t.TempDir(), SeedAfterComplete: true}
	st := &state.State{}
	st.Upsert(state.Entry{
		Magnet:      "magnet:?xt=urn:btih:def",
		Name:        "legacy",
		AddedAt:     time.Now().UTC(),
		NeedsRelink: true,
	})
	app := &App{cfg: cfg, st: st}

	items := app.downloadItems()
	if len(items) != 1 {
		t.Fatalf("downloadItems returned %d items, want 1", len(items))
	}
	if items[0].State != engine.StateMissing {
		t.Fatalf("state = %s, want missing data", items[0].State)
	}
}

func TestDeleteDownloadDataRefusesUnsafePath(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := deleteDownloadData(downloadItem{DownloadDir: dir, DataPath: outside})
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("deleteDownloadData err = %v, want unsafe refusal", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("unsafe path was removed: %v", err)
	}
}

func TestMovePayloadMovesPartFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "image.iso")
	newPath := filepath.Join(dir, "moved", "image.iso")
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath+".part", []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := movePayload(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath + ".part"); !os.IsNotExist(err) {
		t.Fatalf("old part still exists: %v", err)
	}
	got, err := os.ReadFile(newPath + ".part")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "partial" {
		t.Fatalf("moved part = %q", got)
	}
}
