package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

func (a *App) updateOrganizationPlan(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := a.downloads.organizationPlan
	if p.menu != nil {
		switch key.String() {
		case "esc":
			p.menu = nil
		case "up", "k":
			p.menu.win.move(-1, len(p.menu.options), max(1, a.bodyHeight()-2))
		case "down", "j":
			p.menu.win.move(1, len(p.menu.options), max(1, a.bodyHeight()-2))
		case "enter":
			p.moves[p.win.cursor].choose(a.cfg, p.menu.options[p.menu.win.cursor].ID, "manual")
			p.menu = nil
		}
		return a, nil
	}
	switch key.String() {
	case "esc":
		a.downloads.organizationPlan = nil
	case "up", "k":
		p.win.move(-1, len(p.moves), a.organizationPlanRows())
	case "down", "j":
		p.win.move(1, len(p.moves), a.organizationPlanRows())
	case "c":
		if len(p.moves) > 0 {
			p.menu = &categoryMenu{options: a.cfg.Organization.Categories()}
			for i, category := range p.menu.options {
				if category.ID == p.moves[p.win.cursor].destination.Category {
					p.menu.win.cursor = i
				}
			}
		}
	case "enter":
		for _, move := range p.moves {
			if move.err != nil {
				return a, a.showError(move.err.Error())
			}
		}
		p.next, p.completed = 0, 0
		p.running = true
		return a, a.nextOrganizationMove()
	}
	return a, nil
}

func (a *App) nextOrganizationMove() tea.Cmd {
	p := a.downloads.organizationPlan
	for p.next < len(p.moves) {
		i := p.next
		move := p.moves[i]
		it, ok := a.downloadItemByMagnet(a.buildDownloadItems(), move.item.Magnet)
		if !ok {
			p.running = false
			return a.showError("download is no longer saved")
		}
		if !move.changed(it) {
			p.next++
			continue
		}
		p.win.cursor = i
		cmd := a.startCategoryMove(it, move.destination, move.source)
		if !a.downloads.moving {
			p.running = false
		}
		return cmd
	}
	a.downloads.organizationPlan = nil
	return a.showToast(fmt.Sprintf("organized %d downloads", p.completed), toastOK, toastQuick)
}
