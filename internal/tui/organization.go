package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/state"
)

type organizationSavedMsg struct {
	enabled bool
	err     error
}

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

func (a *App) organizationLabel() string {
	if a.downloads.savingOrganization {
		return "folders: saving…"
	}
	if a.cfg != nil && a.cfg.Organization.Enabled {
		return "folders: on"
	}
	return "folders: off"
}

func (a *App) toggleOrganization() tea.Cmd {
	if a.cfg == nil || a.downloads.savingOrganization {
		return nil
	}
	a.downloads.savingOrganization = true
	enabled := !a.cfg.Organization.Enabled
	cfg := *a.cfg
	return func() tea.Msg {
		return organizationSavedMsg{enabled: enabled, err: cfg.WriteOrganizationEnabled(enabled)}
	}
}

func (a *App) onOrganizationSaved(msg organizationSavedMsg) tea.Cmd {
	a.downloads.savingOrganization = false
	if msg.err != nil {
		return a.showError("could not save folder organization: " + msg.err.Error())
	}
	a.cfg.Organization.Enabled = msg.enabled
	text := "folder organization off · new downloads use the main folder"
	if msg.enabled {
		text = "folder organization on · new downloads use category folders"
	}
	return a.showToast(text, toastOK, toastQuick)
}
