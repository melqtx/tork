package tui

import (
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"
)

type uiPreferences struct {
	SearchSort       string  `json:"search_sort"`
	SearchCategory   string  `json:"search_category"`
	HideDead         bool    `json:"hide_zero_seeders,omitempty"`
	DownloadCategory *string `json:"download_category,omitempty"`
}

func (a *App) restorePreferences() {
	if a.st == nil {
		return
	}
	var p uiPreferences
	if json.Unmarshal(a.st.Preferences, &p) != nil {
		return
	}
	for _, mode := range []sortMode{sortScore, sortSeeders, sortSize} {
		if mode.String() == p.SearchSort {
			a.results.sort = mode
		}
	}
	for i, name := range browseCategories {
		if name == p.SearchCategory {
			a.results.category = i
			a.results.navCursor = i
		}
	}
	a.results.hideDead = p.HideDead
	if p.DownloadCategory != nil {
		for _, category := range a.cfg.Organization.Categories() {
			if category.ID == *p.DownloadCategory {
				id := category.ID
				a.downloads.categoryFilter = &id
				break
			}
		}
	}
}

func (a *App) savePreferences() tea.Cmd {
	if a.st == nil || a.cfg == nil {
		return nil
	}
	p := uiPreferences{SearchSort: a.results.sort.String(), HideDead: a.results.hideDead}
	if i := a.results.category; i >= 0 && i < len(browseCategories) {
		p.SearchCategory = browseCategories[i]
	}
	if a.downloads.categoryFilter != nil {
		id := *a.downloads.categoryFilter
		p.DownloadCategory = &id
	}
	data, err := json.Marshal(p)
	if err != nil {
		return a.showError("save preferences: " + err.Error())
	}
	a.st.Preferences = data
	return a.saveState()
}
