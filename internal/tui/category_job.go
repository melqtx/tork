package tui

import (
	"errors"
	"path/filepath"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/state"
)

type categoryMovedMsg struct {
	entry state.Entry
	err   error
}

func (a *App) startCategoryMove(it downloadItem, destination config.DownloadDestination, source string) tea.Cmd {
	if a.pendingAdds > 0 {
		return a.showError("wait for the pending download to finish queuing")
	}
	cfg := *a.cfg
	saved := &state.State{Entries: slices.Clone(a.st.Entries)}
	worker := &App{cfg: &cfg, eng: a.eng, st: saved, downloads: newDownloadsModel()}
	a.downloads.moving = true
	return func() tea.Msg {
		e := saved.Find(it.Magnet)
		if e == nil {
			return categoryMovedMsg{err: errors.New("download is no longer saved")}
		}
		finish := func(err error) tea.Msg { return categoryMovedMsg{entry: *e, err: err} }
		if err := cfg.PrepareDownloadDestination(destination); err != nil {
			return finish(err)
		}
		if filepath.Clean(it.DownloadDir) == filepath.Clean(destination.DownloadDir) {
			e.Category, e.CategorySource = destination.Category, source
			return finish(nil)
		}
		if it.Live {
			if err := worker.eng.Pause(it.Hash); err != nil {
				return finish(err)
			}
			if snap, ok := worker.eng.Snapshot(it.Hash); ok {
				applySnapshotToEntry(e, snap)
				it.DataPath, it.Name = snap.DataPath, snap.Name
				it.BytesCompleted, it.Length = snap.BytesCompleted, snap.Length
			}
		}
		target := &state.MoveTarget{DownloadDir: destination.DownloadDir,
			Category: destination.Category, CategorySource: source}
		if it.DataPath != "" {
			target.DataPath = filepath.Join(destination.DownloadDir, filepath.Base(it.DataPath))
		}
		e.PendingMove = target
		if err := saved.Save(cfg.StatePath()); err != nil {
			e.PendingMove = nil
			if it.Live && shouldResumeAfterPathChange(it) {
				err = errors.Join(err, worker.eng.Resume(it.Hash))
			}
			return finish(err)
		}
		err := worker.moveDownload(it, destination.DownloadDir)
		if e.DownloadDir == destination.DownloadDir {
			target.Apply(e)
		}
		e.PendingMove = nil
		return finish(err)
	}
}

func (a *App) onCategoryMoved(msg categoryMovedMsg) tea.Cmd {
	a.downloads.moving = false
	if e := a.st.Find(msg.entry.Magnet); e != nil {
		*e = msg.entry
	}
	a.downloads.snaps = a.eng.Snapshots()
	a.refreshDownloadItems()
	if err := a.st.Save(a.cfg.StatePath()); err != nil {
		return a.showError("save failed: " + err.Error())
	}
	if msg.err != nil {
		return tea.Batch(a.showError(msg.err.Error()), a.ensureTick())
	}
	a.downloads.categoryMove = nil
	return tea.Batch(a.showToast("category updated · files in place", toastOK, toastQuick), a.ensureTick())
}
