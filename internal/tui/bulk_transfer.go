package tui

import (
	"errors"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

type bulkTransferMsg struct {
	entry state.Entry
	err   error
}

func (a *App) bulkTransferCmd(it downloadItem, pause bool) tea.Cmd {
	cfg, saved := *a.cfg, *a.st
	saved.Entries = slices.Clone(a.st.Entries)
	worker := &App{cfg: &cfg, eng: a.eng, st: &saved}
	return func() tea.Msg {
		e := saved.Find(it.Magnet)
		if e == nil {
			return bulkTransferMsg{err: errors.New("download is no longer saved")}
		}
		hash := it.Hash
		var err error
		if pause {
			err = worker.eng.Pause(hash)
		} else {
			hash, err = worker.activateDownload(it)
		}
		if err == nil {
			e.Paused = pause
			if snap, ok := worker.eng.Snapshot(hash); ok {
				applySnapshotToEntry(e, snap)
				if snap.State == engine.StateDone || snap.State == engine.StateSeeding {
					e.Done = true
					if e.CompletedAt == nil {
						now := time.Now().UTC()
						e.CompletedAt = &now
					}
				}
			}
		}
		return bulkTransferMsg{entry: *e, err: err}
	}
}
