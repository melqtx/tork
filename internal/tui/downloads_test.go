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

func TestClipboardSequenceCopiesFullText(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("STY", "")

	got := clipboardSequence("/home/cat/Downloads/image.iso").String()
	want := "\x1b]52;c;L2hvbWUvY2F0L0Rvd25sb2Fkcy9pbWFnZS5pc28=\x07"
	if got != want {
		t.Fatalf("clipboard sequence = %q, want %q", got, want)
	}
}

func TestOverlayBottomRightKeepsLeftContent(t *testing.T) {
	base := strings.Join([]string{"aa", "bb", "cc", "dd", "ee"}, "\n")
	got := strings.Split(overlayBottomRight(base, "XX\nYY", 8), "\n")

	if len(got) != 5 {
		t.Fatalf("line count = %d, want 5", len(got))
	}
	if got[0] != "aa" || got[1] != "bb" || got[2] != "cc" {
		t.Fatalf("untouched lines changed: %q", got)
	}
	if !strings.HasPrefix(got[3], "dd") || !strings.HasSuffix(got[3], "XX") {
		t.Fatalf("toast row 1 = %q, want left content kept and XX right-aligned", got[3])
	}
	if !strings.HasPrefix(got[4], "ee") || !strings.HasSuffix(got[4], "YY") {
		t.Fatalf("toast row 2 = %q, want left content kept and YY right-aligned", got[4])
	}
}
