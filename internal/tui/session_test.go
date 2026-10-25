package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/session"
	"github.com/melqtx/tork/internal/state"
)

func TestBackgroundPreviewStillNeedsApproval(t *testing.T) {
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.OverrideDownloadDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	magnet := "magnet:?xt=urn:btih:1111111111111111111111111111111111111111&dn=Unapproved"
	h, owned, err := eng.AddForPreview(magnet)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, eng, nil, &state.State{}, nil)
	a.screen = screenPreview
	a.preview = newPreviewModel(h, magnet, "Unapproved", screenSearch, owned)
	a.DetachSession()
	if err := a.CheckpointSession(); err != nil {
		t.Fatal(err)
	}
	saved, err := state.Load(cfg.StatePath())
	if err != nil || len(saved.Entries) != 0 {
		t.Fatal("detaching silently approved a preview")
	}
	if _, err := a.AttachSession(nil); err != nil {
		t.Fatal(err)
	}
	if a.screen != screenPreview || a.preview.hash != h {
		t.Fatal("reattach lost the pending preview")
	}
	a.startPreviewDownload()
	if err := a.CheckpointSession(); err != nil {
		t.Fatal(err)
	}
	saved, err = state.Load(cfg.StatePath())
	if err != nil || len(saved.Entries) != 1 || saved.Entries[0].Magnet != magnet {
		t.Fatal("approved transfer was not persisted")
	}
}

func TestBackgroundCopyWaitsForAttachedTerminalResult(t *testing.T) {
	a := sourcesTestApp(t)
	a.background = true
	msg := a.yankDownloadValue("magnet", "magnet:test")()
	request, ok := msg.(session.ClipboardRequest)
	if !ok || request.Text != "magnet:test" {
		t.Fatal("background copy bypassed the terminal client")
	}
	if result, ok := request.Reply(nil).(yankDoneMsg); !ok || result.err != nil {
		t.Fatal("successful acknowledgement was not delivered")
	}
	if result, ok := request.Reply(errors.New("terminal disconnected")).(yankDoneMsg); !ok || result.err == nil {
		t.Fatal("failed terminal copy reported success")
	}
}

func TestClosingUIInvalidatesLateClipboardLookup(t *testing.T) {
	a := sourcesTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.results.resolving = true
	a.results.resolveYank = true
	a.results.resolveID = 7
	a.results.resolveCancel = cancel
	a.DetachSession()
	if a.results.resolving || ctx.Err() == nil {
		t.Fatal("closed terminal retained a clipboard action")
	}
	_, cmd := a.Update(magnetResolvedMsg{searchID: a.results.searchID, resolveID: 7, yank: true, magnet: "private magnet"})
	if cmd != nil {
		t.Fatal("late clipboard lookup acted after the terminal closed")
	}
}

func TestSessionStopWaitsForApprovedAddToBeRecorded(t *testing.T) {
	a := &App{pendingAdds: 1}
	if err := a.CheckpointSession(); err == nil || !strings.Contains(err.Error(), "still being queued") {
		t.Fatal("stop discarded a pending approved add")
	}
}
