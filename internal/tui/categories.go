package tui

import (
	"strings"

	"github.com/anacrolix/torrent/metainfo"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
)

type categoryMenu struct {
	options []config.Category
	win     listWindow
	editor  *categoryEditor
}

type previewCategoryChangedMsg struct {
	hash        metainfo.Hash
	destination config.DownloadDestination
	err         error
}

func (a *App) openPreviewCategories() tea.Cmd {
	if !a.preview.owned {
		return a.showToast("already added · change it from downloads", toastOK, toastQuick)
	}
	m := &categoryMenu{options: a.cfg.Organization.Categories()}
	for i, category := range m.options {
		if category.ID == a.preview.category {
			m.win.cursor = i
		}
	}
	a.preview.categories = m
	return nil
}

func (a *App) updatePreviewCategories(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := a.preview.categories
	switch key.String() {
	case "a":
		return a, a.openCategoryEditor()
	case "esc":
		a.preview.categories = nil
	case "up", "k":
		m.win.move(-1, len(m.options), max(1, a.bodyHeight()-2))
	case "down", "j":
		m.win.move(1, len(m.options), max(1, a.bodyHeight()-2))
	case "enter":
		destination, err := a.cfg.ResolveCategoryDestination(m.options[m.win.cursor].ID)
		if err != nil {
			return a, a.showError(err.Error())
		}
		a.preview.changingCategory = true
		hash, cfg := a.preview.hash, *a.cfg
		return a, func() tea.Msg {
			err := cfg.PrepareDownloadDestination(destination)
			if err == nil {
				err = a.eng.SetPreviewDownloadDir(hash, destination.DownloadDir)
			}
			return previewCategoryChangedMsg{hash: hash, destination: destination, err: err}
		}
	}
	return a, nil
}

func (a *App) onPreviewCategoryChanged(msg previewCategoryChangedMsg) tea.Cmd {
	if msg.hash != a.preview.hash {
		return nil
	}
	a.preview.changingCategory = false
	if msg.err != nil {
		return a.showError(msg.err.Error())
	}
	a.preview.category, a.preview.saveDir = msg.destination.Category, msg.destination.DownloadDir
	a.preview.categories = nil
	return nil
}

func (a *App) viewCategoryMenu(m *categoryMenu) string {
	if m.editor != nil {
		return a.viewCategoryEditor(m.editor)
	}
	width := a.contentWidth()
	var b strings.Builder
	b.WriteString(styleTitle.Render("Choose category") + "\n\n")
	b.WriteString(renderWindow(&m.win, len(m.options), max(1, a.bodyHeight()-2), width,
		func(i int, selected bool) string { return m.options[i].Name }))
	return a.chrome("categories", b.String(),
		hints(hint("↑↓", "choose"), hint("enter", "apply"), hint("a", "add category"), hint("esc", "back")))
}
