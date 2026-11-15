package tui

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
)

type downloadCategoryMove struct {
	item        downloadItem
	menu        *categoryMenu
	destination *config.DownloadDestination
}

func (a *App) openDownloadCategoryMove(it downloadItem) tea.Cmd {
	metadataOnly := it.State == engine.StateFetchingMeta && it.BytesCompleted == 0
	if a.st.Find(it.Magnet) == nil || (!metadataOnly && !safeDownloadPath(it.DownloadDir, it.DataPath)) {
		return a.showError("relink this download before changing its category")
	}
	m := &categoryMenu{options: a.cfg.Organization.Categories()}
	for i, option := range m.options {
		if option.ID == it.Category {
			m.win.cursor = i
		}
	}
	a.downloads.categoryMove = &downloadCategoryMove{item: it, menu: m}
	return nil
}

func (a *App) updateDownloadCategoryMove(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	m := a.downloads.categoryMove
	if key.String() == "esc" {
		if m.destination != nil {
			m.destination = nil
		} else {
			a.downloads.categoryMove = nil
		}
		return a, nil
	}
	if m.destination == nil {
		switch key.String() {
		case "up", "k":
			m.menu.win.move(-1, len(m.menu.options), max(1, a.bodyHeight()-2))
		case "down", "j":
			m.menu.win.move(1, len(m.menu.options), max(1, a.bodyHeight()-2))
		case "enter":
			destination, err := a.cfg.ResolveCategoryDestination(m.menu.options[m.menu.win.cursor].ID)
			if err != nil {
				return a, a.showError(err.Error())
			}
			m.destination = &destination
		}
		return a, nil
	}
	if key.String() != "enter" {
		return a, nil
	}
	it, ok := a.downloadItemByMagnet(a.buildDownloadItems(), m.item.Magnet)
	if !ok {
		a.downloads.categoryMove = nil
		return a, a.showError("download is no longer in the list")
	}
	return a, a.startCategoryMove(it, *m.destination, "manual")
}

func (a *App) viewDownloadCategoryMove(m *downloadCategoryMove) string {
	if m.destination == nil {
		return a.viewCategoryMenu(m.menu)
	}
	if a.downloads.moving {
		return a.chrome("organize download", styleDim.Render("Moving files…"), "please wait")
	}
	width := a.contentWidth()
	path := filepath.Join(m.destination.DownloadDir, filepath.Base(m.item.DataPath))
	body := strings.Join([]string{
		styleTitle.Render("Move this download?"), "",
		truncate(m.item.Name, width), "",
		"From", styleDim.Render(truncate(m.item.DataPath, width)), "",
		"To", styleFg.Render(truncate(path, width)),
	}, "\n")
	return a.chrome("organize download", body, hints(hint("enter", "move files"), hint("esc", "back")))
}
