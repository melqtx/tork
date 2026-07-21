package tui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	tea "github.com/charmbracelet/bubbletea"
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

func TestVerificationGuardsDoNotPersistRefusedPauseOrSeed(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	magnet := "magnet:?xt=urn:btih:" + strings.Repeat("c", 40)
	h, err := eng.Add(magnet, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	verifyErr := make(chan error, 1)
	go func() {
		_, err := eng.Verify(ctx, h)
		verifyErr <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if snap, ok := eng.Snapshot(h); ok && snap.State == engine.StateVerifying {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("verification did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	st := &state.State{}
	st.Upsert(state.Entry{Magnet: magnet, Seed: state.Bool(false)})
	app := &App{cfg: cfg, eng: eng, st: st}
	stale := downloadItem{Hash: h, Magnet: magnet, State: engine.StateDone, Live: true}
	app.togglePause(stale)
	if st.Entries[0].Paused || !strings.Contains(app.errText, "verification") {
		t.Fatalf("pause guard persisted=%v err=%q", st.Entries[0].Paused, app.errText)
	}
	app.toggleSeed(stale)
	if *st.Entries[0].Seed || !strings.Contains(app.errText, "verification") {
		t.Fatalf("seed guard persisted=%v err=%q", *st.Entries[0].Seed, app.errText)
	}
	cancel()
	<-verifyErr
}

func TestVerifyPersistedDirectUsesActivatedHashAndShowsResult(t *testing.T) {
	t.Setenv("XDG_DOWNLOAD_DIR", filepath.Join(t.TempDir(), "Downloads"))
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), ".tork"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	payload := []byte("persisted ISO")
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.DownloadDir, "image.iso")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	url := "https://example.invalid/image.iso"
	st := &state.State{}
	st.Upsert(state.Entry{Magnet: url, Name: "image.iso", SHA256: fmt.Sprintf("%x", sha256.Sum256(payload)), Done: true, DownloadDir: cfg.DownloadDir, DataPath: path})
	app := New(cfg, eng, nil, st, nil)
	it := app.itemFromEntry(&st.Entries[0], 0)
	cmd := app.verifyDownload(it)
	if cmd == nil {
		t.Fatal("verify command is nil")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("verify command message = %T, want tea.BatchMsg", msg)
	}
	var done verifyDoneMsg
	found := false
	for _, child := range batch {
		if msg := child(); msg != nil {
			if verifyMsg, ok := msg.(verifyDoneMsg); ok {
				done, found = verifyMsg, true
			}
		}
	}
	if !found || done.err != nil || done.hash == (metainfo.Hash{}) {
		t.Fatalf("verify result found=%v msg=%+v", found, done)
	}
	app.onVerifyDone(done)
	if app.verifyNotice != "verified - all data is valid" || app.verifyNoticeWarn {
		t.Fatalf("verify notice=%q warn=%v", app.verifyNotice, app.verifyNoticeWarn)
	}
}

func TestApplyVerifyResultClearsCompletionState(t *testing.T) {
	now := time.Now()
	entry := state.Entry{Done: true, Paused: true, BytesCompleted: 42, CompletedAt: &now}
	if !applyVerifyResultToEntry(&entry, engine.VerifyResult{NeedsRepair: true, BadPieces: 1}) {
		t.Fatal("torrent repair did not report a state change")
	}
	if entry.Done || entry.CompletedAt != nil || entry.Paused || entry.BytesCompleted != 42 {
		t.Fatalf("torrent repair entry = %+v", entry)
	}
	entry.Done, entry.BytesCompleted, entry.CompletedAt = true, 42, &now
	if !applyVerifyResultToEntry(&entry, engine.VerifyResult{NeedsRepair: true, ChecksumMismatch: true}) {
		t.Fatal("direct mismatch did not report a state change")
	}
	if entry.Done || entry.CompletedAt != nil || !entry.Paused || entry.BytesCompleted != 0 {
		t.Fatalf("direct mismatch entry = %+v", entry)
	}
}
