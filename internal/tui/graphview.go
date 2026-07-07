package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/melqtx/tork/internal/provider"
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
	if n := len(r.navItems()); r.gwin.cursor >= n {
		r.gwin.cursor = max(0, n-1)
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
	rows := a.listRows()
	switch msg.String() {
	case "up", "k":
		r.gwin.move(-1, len(items), rows)
	case "down", "j":
		r.gwin.move(1, len(items), rows)
	case "pgup":
		r.gwin.move(-rows, len(items), rows)
	case "pgdown":
		r.gwin.move(rows, len(items), rows)
	case "g", "home":
		r.gwin.home()
	case "G", "end":
		r.gwin.end(len(items), rows)
	case "left", "h":
		if it, ok := currentItem(items, r.gwin.cursor); ok && len(r.groups[it.group].rowIdx) > 1 {
			r.groups[it.group].collapsed = true
			r.clampGCursor()
		}
	case "right", "l":
		if it, ok := currentItem(items, r.gwin.cursor); ok {
			r.groups[it.group].collapsed = false
		}
	case "enter":
		it, ok := currentItem(items, r.gwin.cursor)
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
	if n := len(r.navItems()); r.gwin.cursor >= n {
		r.gwin.cursor = max(0, n-1)
	}
}

func (a *App) viewGraph(width, h int) string {
	r := &a.results
	items := r.navItems()
	return renderWindow(&r.gwin, len(items), h, width, func(i int, selected bool) string {
		it := items[i]
		g := &r.groups[it.group]
		switch {
		case len(g.rowIdx) == 1:
			return r.graphFlatLeaf(g.rowIdx[0], width)
		case it.leaf == -1:
			return r.graphHeader(g, width)
		default:
			return r.graphLeaf(g, it.leaf, width)
		}
	})
}

func (r *resultsModel) graphHeader(g *group, width int) string {
	arrow := "▾"
	if g.collapsed {
		arrow = "▸"
	}
	meta := styleDim.Render(fmt.Sprintf("· %d sources", len(g.rowIdx)))
	titleW := max(12, width-lipglossWidth(meta)-4)
	return fmt.Sprintf("%s %s %s",
		styleTitle.Render(arrow),
		truncate(g.label, titleW),
		meta,
	)
}

func (r *resultsModel) graphLeaf(g *group, leaf, width int) string {
	branch := "├─"
	if leaf == len(g.rowIdx)-1 {
		branch = "└─"
	}
	lay := newGraphLayout(width)
	row := r.rows[g.rowIdx[leaf]]
	bar := seederBar(row.res.Seeders, groupMaxSeeders(r, g), lay.barW)
	if bar != "" {
		bar += " "
	}
	return fmt.Sprintf("  %s %s %s %s%s %s  %s",
		styleDim.Render(branch),
		healthDot(row.res.Seeders),
		padRight(providerTag(row.res.Provider), lay.provW),
		bar,
		padRight(styleSeeders.Render(fmt.Sprintf("S%d", row.res.Seeders)), lay.seedW),
		fmt.Sprintf("%*s", lay.sizeW, truncate(row.res.Size, lay.sizeW)),
		leafMarkers(row.res, leaf == 0),
	)
}

// leafMarkers is the trailing status glyphs: a gold star for the best source,
// a check for a trusted upload, and a faint "resolve" only when there's no
// ready magnet (the common magnet-ready case needs no label).
func leafMarkers(res provider.Result, best bool) string {
	var m []string
	if best {
		m = append(m, styleBest.Render("★"))
	}
	if res.Trusted {
		m = append(m, styleOK.Render("✓"))
	}
	if res.Magnet == "" {
		m = append(m, styleFaint.Render("resolve"))
	}
	return strings.Join(m, " ")
}

// graphFlatLeaf renders a single-source group as a dense one-liner: the title
// flexes, then the same provider/seeders/size columns as a leaf row.
func (r *resultsModel) graphFlatLeaf(idx, width int) string {
	lay := newGraphLayout(width)
	row := r.rows[idx]
	bar := seederBar(row.res.Seeders, row.res.Seeders, lay.barW)
	if bar != "" {
		bar += " "
	}
	return fmt.Sprintf("%s %s %s %s%s %s  %s",
		healthDot(row.res.Seeders),
		padRight(truncate(row.res.Title, lay.titleW), lay.titleW),
		padRight(providerTag(row.res.Provider), lay.provW),
		bar,
		padRight(styleSeeders.Render(fmt.Sprintf("S%d", row.res.Seeders)), lay.seedW),
		fmt.Sprintf("%*s", lay.sizeW, truncate(row.res.Size, lay.sizeW)),
		leafMarkers(row.res, false),
	)
}

func (a *App) graphDetail(width int) string {
	r := &a.results
	items := r.navItems()
	lines := []string{rule(width)}
	it, ok := currentItem(items, r.gwin.cursor)
	if !ok || it.group >= len(r.groups) {
		lines = append(lines, styleDim.Render("no source selected"), "", "", "")
		return strings.Join(lines, "\n")
	}
	g := &r.groups[it.group]
	if it.leaf == -1 {
		median, spread := groupSizeSpread(r.rows, g.rowIdx)
		lines = append(lines,
			styleFg.Render(truncate(g.label, width)),
			styleDim.Render(fmt.Sprintf("%d sources · %d total seeders", len(g.rowIdx), groupTotalSeeders(r, g))),
			formatSizeSpread(median, spread),
			"",
		)
		return strings.Join(lines, "\n")
	}
	row := r.rows[g.rowIdx[it.leaf]]
	median, spread := groupSizeSpread(r.rows, g.rowIdx)
	magnet := styleOK.Render("magnet ready")
	if row.res.Magnet == "" {
		magnet = styleFaint.Render("needs resolve")
	}
	trusted := styleFaint.Render("unverified")
	if row.res.Trusted {
		trusted = styleOK.Render("✓trusted")
	}
	lines = append(lines,
		styleFg.Render(truncate(row.res.Title, width)),
		fmt.Sprintf("%s  %s  %s", providerTag(row.res.Provider), trusted, magnet),
		fmt.Sprintf("%s %d   %s %d", styleSeeders.Render("S"), row.res.Seeders, styleLeechers.Render("L"), row.res.Leechers),
		formatSizeSpread(median, spread),
	)
	return strings.Join(lines, "\n")
}

func groupTotalSeeders(r *resultsModel, g *group) int {
	total := 0
	for _, idx := range g.rowIdx {
		total += r.rows[idx].res.Seeders
	}
	return total
}

func graphBarCells(width int) int {
	switch {
	case width < 72:
		return 0
	case width < 88:
		return 8
	default:
		return 12
	}
}

func seederBar(seeders, maxSeeders, cells int) string {
	if cells < 3 {
		return ""
	}
	if maxSeeders <= 0 || seeders <= 0 {
		return styleFaint.Render(strings.Repeat("░", cells))
	}
	filled := int(math.Round(float64(cells) * math.Log2(float64(1+seeders)) / math.Log2(float64(1+maxSeeders))))
	filled = max(1, min(cells, filled))
	return seederBarStyle(seeders).Render(strings.Repeat("▓", filled)) +
		styleFaint.Render(strings.Repeat("░", cells-filled))
}

func seederBarStyle(seeders int) lipgloss.Style {
	switch {
	case seeders >= 50:
		return styleHealthGood
	case seeders >= 5:
		return styleHealthMid
	default:
		return styleHealthBad
	}
}

func groupMaxSeeders(r *resultsModel, g *group) int {
	maxSeeders := 0
	for _, idx := range g.rowIdx {
		maxSeeders = max(maxSeeders, r.rows[idx].res.Seeders)
	}
	return maxSeeders
}

func groupSizeSpread(rows []scoredRow, idxs []int) (median int64, maxDevPct float64) {
	var sizes []int64
	for _, idx := range idxs {
		if idx >= 0 && idx < len(rows) && rows[idx].res.SizeBytes > 0 {
			sizes = append(sizes, rows[idx].res.SizeBytes)
		}
	}
	if len(sizes) == 0 {
		return 0, 0
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
	if len(sizes)%2 == 0 {
		median = (sizes[len(sizes)/2-1] + sizes[len(sizes)/2]) / 2
	} else {
		median = sizes[len(sizes)/2]
	}
	if median <= 0 {
		return median, 0
	}
	for _, s := range sizes {
		dev := math.Abs(float64(s-median)) / float64(median) * 100
		if dev > maxDevPct {
			maxDevPct = dev
		}
	}
	return median, maxDevPct
}

func formatSizeSpread(median int64, spread float64) string {
	if median == 0 {
		return styleFaint.Render("size unknown")
	}
	if spread <= 5 {
		return styleOK.Render("sizes agree") + styleFaint.Render(" · median ") + styleDim.Render(humanBytes(median))
	}
	return styleHealthMid.Render(fmt.Sprintf("size varies ±%.0f%%", spread)) +
		styleFaint.Render(" · median ") + styleDim.Render(humanBytes(median))
}

func lipglossWidth(s string) int {
	return lipgloss.Width(s)
}
