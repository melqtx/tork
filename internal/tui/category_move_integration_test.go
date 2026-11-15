//go:build integration

package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/engine"
)

func TestCategoryMovePreservesSeedingAndRecoversFromCollision(t *testing.T) {
	a, res, payload := organizedTorrentAppWithSeeding(t, true)
	a.Update(a.actOnResult(res, false)())
	deadline := time.Now().Add(10 * time.Second)
	for a.eng.Snapshots()[0].State != engine.StateSeeding {
		if time.Now().After(deadline) {
			t.Fatal("local torrent did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.downloads.snaps = a.eng.Snapshots()
	a.syncCompletedToState()
	a.refreshDownloadItems()
	it := a.downloadItems()[0]
	completed := *a.st.Entries[0].CompletedAt
	a.openDownloadCategoryMove(it)
	for i, option := range a.downloads.categoryMove.menu.options {
		if option.ID == "tv" {
			a.downloads.categoryMove.menu.win.cursor = i
		}
	}
	a.updateDownloadCategoryMove(tea.KeyMsg{Type: tea.KeyEnter})
	a.updateDownloadCategoryMove(tea.KeyMsg{Type: tea.KeyEnter})
	path := filepath.Join(a.cfg.DownloadDir, "tv", "payload.dat")
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("move failed: %v; UI error=%q; item=%+v; entry=%+v", err, a.errText, it, a.st.Entries[0])
	}
	entry := a.st.Entries[0]
	if entry.Category != "tv" || !entry.SeedEnabled(false) || !entry.Done || *entry.CompletedAt != completed {
		t.Fatalf("move changed seeding or completion settings: %+v", entry)
	}
	if _, err := os.Stat(it.DataPath); !os.IsNotExist(err) {
		t.Fatalf("old payload was left behind: %v", err)
	}
	destination := filepath.Join(a.cfg.DownloadDir, "games")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "payload.dat"), []byte("existing file"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, ok := a.eng.Snapshot(it.Hash)
	if !ok || !snap.Seed {
		t.Fatal("seeding was not resumed after moving")
	}
	live := itemFromSnapshot(snap, 0, true)
	if err := a.moveDownload(live, destination); err == nil {
		t.Fatal("overwrote a conflicting payload")
	}
	snap, ok = a.eng.Snapshot(it.Hash)
	if !ok || !snap.Seed || snap.DownloadDir != filepath.Dir(path) {
		t.Fatalf("failed move did not restore the original transfer: %+v", snap)
	}
}
