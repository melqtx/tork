package tui

import (
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/state"
)

// Different tracker lists can still point to the same torrent.
func (a *App) findDownloadEntry(magnet string) *state.Entry {
	if a.st == nil {
		return nil
	}
	if entry := a.st.Find(magnet); entry != nil {
		return entry
	}
	hash := (provider.Result{Magnet: magnet}).InfoHash()
	if hash == "" {
		return nil
	}
	for i := range a.st.Entries {
		if (provider.Result{Magnet: a.st.Entries[i].Magnet}).InfoHash() == hash {
			return &a.st.Entries[i]
		}
	}
	return nil
}
