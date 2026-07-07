package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/rank"
)

// group clusters results that are the same content at the same quality across
// providers - the "source graph" of where a title can be pulled from.
type group struct {
	key       string
	label     string
	rowIdx    []int // indices into r.rows, best first
	collapsed bool
}

// navItem is one selectable line in the grouped view: a multi-source header
// (leaf == -1) or a leaf pointing at rowIdx[leaf] of its group.
type navItem struct {
	group int
	leaf  int
}

// rebuildGroups clusters the currently-visible rows, preserving collapse
// state across rebuilds by group key.
func (r *resultsModel) rebuildGroups() {
	prevCollapsed := make(map[string]bool, len(r.groups))
	for _, g := range r.groups {
		prevCollapsed[g.key] = g.collapsed
	}
	visible := make(map[int]bool, len(r.visible))
	for _, idx := range r.visible {
		visible[idx] = true
	}

	byKey := make(map[string]int)
	r.groups = r.groups[:0]
	for idx := range r.rows { // r.rows is globally sorted, so best-first falls out
		if !visible[idx] {
			continue
		}
		row := r.rows[idx]
		key := rank.GroupKey(row.res.Title, row.tags)
		gi, ok := byKey[key]
		if !ok {
			gi = len(r.groups)
			byKey[key] = gi
			r.groups = append(r.groups, group{
				key:       key,
				label:     rank.GroupLabel(row.res.Title, row.tags),
				collapsed: prevCollapsed[key],
			})
		}
		r.groups[gi].rowIdx = append(r.groups[gi].rowIdx, idx)
	}
	if n := len(r.navItems()); r.gcursor >= n {
		r.gcursor = max(0, n-1)
	}
}

func (r *resultsModel) navItems() []navItem {
	var items []navItem
	for gi := range r.groups {
		g := &r.groups[gi]
		if len(g.rowIdx) == 1 { // single source renders as a plain leaf
			items = append(items, navItem{group: gi, leaf: 0})
			continue
		}
		items = append(items, navItem{group: gi, leaf: -1}) // header
		if !g.collapsed {
			for li := range g.rowIdx {
				items = append(items, navItem{group: gi, leaf: li})
			}
		}
	}
	return items
}

func (a *App) updateGraphKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	items := r.navItems()
	switch msg.String() {
	case "up", "k":
		r.gcursor = max(0, r.gcursor-1)
	case "down", "j":
		r.gcursor = min(len(items)-1, r.gcursor+1)
	case "g", "home":
		r.gcursor = 0
	case "G", "end":
		r.gcursor = max(0, len(items)-1)
	case "left", "h":
		if it, ok := currentItem(items, r.gcursor); ok && len(r.groups[it.group].rowIdx) > 1 {
			r.groups[it.group].collapsed = true
			r.clampGCursor()
		}
	case "right", "l":
		if it, ok := currentItem(items, r.gcursor); ok {
			r.groups[it.group].collapsed = false
		}
	case "enter":
		it, ok := currentItem(items, r.gcursor)
		if !ok {
			return a, nil
		}
		g := &r.groups[it.group]
		if it.leaf == -1 { // header toggles collapse
			g.collapsed = !g.collapsed
			r.clampGCursor()
			return a, nil
		}
		return a, a.downloadResult(r.rows[g.rowIdx[it.leaf]].res)
	}
	return a, nil
}

func currentItem(items []navItem, cursor int) (navItem, bool) {
	if cursor < 0 || cursor >= len(items) {
		return navItem{}, false
	}
	return items[cursor], true
}

func (r *resultsModel) clampGCursor() {
	if n := len(r.navItems()); r.gcursor >= n {
		r.gcursor = max(0, n-1)
	}
}

func (a *App) viewGraph(width, h int) string {
	r := &a.results
	items := r.navItems()

	// window the flattened items around the cursor
	offset := 0
	if r.gcursor >= h {
		offset = r.gcursor - h + 1
	}
	offset = max(0, min(offset, max(0, len(items)-h)))
	end := min(len(items), offset+h)

	var b strings.Builder
	for i := offset; i < end; i++ {
		it := items[i]
		g := &r.groups[it.group]
		var line string
		switch {
		case len(g.rowIdx) == 1:
			line = r.graphFlatLeaf(g.rowIdx[0], width)
		case it.leaf == -1:
			line = r.graphHeader(g)
		default:
			last := it.leaf == len(g.rowIdx)-1
			line = r.graphLeaf(g.rowIdx[it.leaf], last)
		}
		if i == r.gcursor {
			line = styleSelected.Render(padRight(line, width))
		}
		b.WriteString(line + "\n")
	}
	for i := end - offset; i < h; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

func (r *resultsModel) graphHeader(g *group) string {
	arrow := "▾"
	if g.collapsed {
		arrow = "▸"
	}
	return fmt.Sprintf(" %s %s %s",
		styleTitle.Render(arrow),
		truncate(g.label, 60),
		styleDim.Render(fmt.Sprintf("· %d sources", len(g.rowIdx))),
	)
}

func (r *resultsModel) graphLeaf(idx int, last bool) string {
	branch := "├─"
	if last {
		branch = "└─"
	}
	row := r.rows[idx]
	trusted := ""
	if row.res.Trusted {
		trusted = " " + styleOK.Render("✓trusted")
	}
	return fmt.Sprintf("   %s %s %-7s %10s  %s%s",
		styleDim.Render(branch),
		healthDot(row.res.Seeders),
		providerTag(row.res.Provider),
		truncate(row.res.Size, 10),
		styleSeeders.Render(fmt.Sprintf("S%d", row.res.Seeders)),
		trusted,
	)
}

// graphFlatLeaf renders a single-source group as a dense one-liner.
func (r *resultsModel) graphFlatLeaf(idx, width int) string {
	row := r.rows[idx]
	title := truncate(row.res.Title, max(20, width-34))
	return fmt.Sprintf(" %s %s %-7s %10s  %s",
		healthDot(row.res.Seeders),
		title,
		providerTag(row.res.Provider),
		truncate(row.res.Size, 10),
		styleSeeders.Render(fmt.Sprintf("S%d", row.res.Seeders)),
	)
}
