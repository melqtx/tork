package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
)

type organizationMove struct {
	item        downloadItem
	destination config.DownloadDestination
	source      string
	err         error
}

type organizationPlan struct {
	moves     []organizationMove
	win       listWindow
	menu      *categoryMenu
	running   bool
	next      int
	completed int
}

func (m *organizationMove) choose(cfg *config.Config, category, source string) {
	m.destination, m.err = cfg.ResolveCategoryDestination(category)
	m.source = source
	if m.item.State == engine.StateMissing {
		m.err = fmt.Errorf("relink this download first")
	}
}

func (m organizationMove) changed(it downloadItem) bool {
	return filepath.Clean(it.DownloadDir) != filepath.Clean(m.destination.DownloadDir) ||
		it.Category != m.destination.Category || it.CategorySource != m.source
}

func (a *App) buildOrganizationPlan() *organizationPlan {
	plan := &organizationPlan{}
	for _, item := range a.buildDownloadItems() {
		if a.st.Find(item.Magnet) == nil {
			continue
		}
		move := organizationMove{item: item}
		move.choose(a.cfg, item.Category, item.CategorySource)
		plan.moves = append(plan.moves, move)
	}
	return plan
}

func (a *App) organizationPlanRows() int { return max(1, (a.bodyHeight()-2)/2) }

func (a *App) viewOrganizationPlan(plan *organizationPlan) string {
	if plan.menu != nil {
		return a.viewCategoryMenu(plan.menu)
	}
	width := a.contentWidth()
	var body strings.Builder
	body.WriteString(styleTitle.Render("Organize existing downloads") + "\n\n")
	if len(plan.moves) == 0 {
		body.WriteString(styleDim.Render("No saved downloads."))
	}
	start, end := plan.win.clamp(len(plan.moves), a.organizationPlanRows())
	for i := start; i < end; i++ {
		m := plan.moves[i]
		prefix := "  "
		if i == plan.win.cursor {
			prefix = styleBrand.Render("▍ ")
		}
		label := m.item.Name + " · " + a.downloadCategoryLabel(m.destination.Category)
		body.WriteString(prefix + truncate(label, max(1, width-2)) + "\n")
		pathWidth := max(1, (width-6)/2)
		from := organizationFolder(m.item.DownloadDir, pathWidth)
		to := organizationFolder(m.destination.DownloadDir, pathWidth)
		line := from + " → " + to
		if m.err != nil {
			line = m.err.Error()
		} else if !m.changed(m.item) {
			line = "already here"
		}
		body.WriteString("  " + styleDim.Render(truncate(line, max(1, width-2))))
		if i < end-1 {
			body.WriteString("\n")
		}
	}
	help := hints(hint("↑↓", "select"), hint("c", "category"), hint("enter", "apply moves"), hint("esc", "cancel"))
	if plan.running {
		help = fmt.Sprintf("organizing · %d finished", plan.completed)
	}
	return a.chrome("organize downloads", body.String(), help)
}

func organizationFolder(path string, width int) string {
	path = filepath.ToSlash(path)
	if length := ansi.StringWidth(path); length > width {
		return ansi.TruncateLeft(path, length-width+1, "…")
	}
	return path
}
