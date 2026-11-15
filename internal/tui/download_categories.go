package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
)

func (a *App) openDownloadCategoryFilter() {
	options := append([]config.Category{{ID: "\x00", Name: "All downloads"}}, a.cfg.Organization.Categories()...)
	m := &categoryMenu{options: options}
	if a.downloads.categoryFilter != nil {
		for i, option := range options {
			if option.ID == *a.downloads.categoryFilter {
				m.win.cursor = i
			}
		}
	}
	a.downloads.categoryMenu = m
}

func (a *App) updateDownloadCategoryFilter(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := a.downloads.categoryMenu
	switch key.String() {
	case "esc":
		a.downloads.categoryMenu = nil
	case "up", "k":
		m.win.move(-1, len(m.options), max(1, a.bodyHeight()-2))
	case "down", "j":
		m.win.move(1, len(m.options), max(1, a.bodyHeight()-2))
	case "enter":
		category := m.options[m.win.cursor].ID
		a.downloads.categoryFilter = nil
		if category != "\x00" {
			a.downloads.categoryFilter = &category
		}
		a.downloads.categoryMenu = nil
		a.downloads.win.home()
		a.downloads.selectionPinned = false
		a.refreshDownloadItems()
	}
	return a, nil
}

func (a *App) filterDownloadItems(items []downloadItem) []downloadItem {
	if a.downloads.categoryFilter == nil {
		return items
	}
	filtered := make([]downloadItem, 0, len(items))
	for _, item := range items {
		if item.Category == *a.downloads.categoryFilter {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func (a *App) downloadCategoryLabel(id string) string {
	if a.cfg != nil {
		for _, category := range a.cfg.Organization.Categories() {
			if category.ID == id {
				return category.Name
			}
		}
	}
	return id
}
