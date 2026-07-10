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
	rowIdx    []int // indices into r.rows; clean sources first (best first), noisy last
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
			collapsed := true // groups arrive collapsed; a user's expand sticks
			if v, ok := prevCollapsed[key]; ok {
				collapsed = v
			}
			r.groups = append(r.groups, group{
				key:       key,
				label:     rank.GroupLabel(row.res.Title, row.tags),
				collapsed: collapsed,
			})
		}
		r.groups[gi].rowIdx = append(r.groups[gi].rowIdx, idx)
	}
	// Within each group, float clean sources above noisy ones (cam / off-topic /
	// language variants), stable within each partition to keep score order. This
	// makes rowIdx[0] the representative used by the header summary, the "best"
	// badge, and enter-to-download - so a noisy source never fronts a group that
	// also has a clean one, and the overall best never hides in a collapsed group.
	for gi := range r.groups {
		idxs := r.groups[gi].rowIdx
		sort.SliceStable(idxs, func(i, j int) bool {
			return !r.rows[idxs[i]].noisy && r.rows[idxs[j]].noisy
		})
	}
	// Sink groups made entirely of noise (cams, dead swarms, off-topic) below
	// every group with at least one clean source, stable so both partitions
	// keep the global best-first order.
	sort.SliceStable(r.groups, func(i, j int) bool {
		return !groupAllNoisy(r, &r.groups[i]) && groupAllNoisy(r, &r.groups[j])
	})
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
	rows := a.graphRows()
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
			r.snapToGroupHeader(it.group) // land on the group we just folded, not a stranger
		}
	case "right", "l":
		if it, ok := currentItem(items, r.gwin.cursor); ok {
			r.groups[it.group].collapsed = false
		}
	case " ":
		if it, ok := currentItem(items, r.gwin.cursor); ok && len(r.groups[it.group].rowIdx) > 1 {
			gi := it.group
			r.groups[gi].collapsed = !r.groups[gi].collapsed
			if r.groups[gi].collapsed {
				r.snapToGroupHeader(gi)
			}
		}
	case "enter":
		it, ok := currentItem(items, r.gwin.cursor)
		if !ok {
			return a, nil
		}
		res, ok := r.graphResultForItem(it)
		if !ok {
			return a, nil
		}
		return a, a.downloadResult(res)
	case "D":
		it, ok := currentItem(items, r.gwin.cursor)
		if !ok {
			return a, nil
		}
		res, ok := r.graphResultForItem(it)
		if !ok {
			return a, nil
		}
		return a, a.downloadResultDirect(res)
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

// snapToGroupHeader moves the cursor onto a group's first nav line (its header,
// or its flat leaf when single-source) after collapsing it, so the cursor
// follows the fold instead of clamping onto whatever row shifted underneath it.
func (r *resultsModel) snapToGroupHeader(gi int) {
	for i, it := range r.navItems() {
		if it.group == gi {
			r.gwin.cursor = i
			return
		}
	}
	r.clampGCursor()
}

func (r *resultsModel) graphResultForItem(it navItem) (provider.Result, bool) {
	if it.group < 0 || it.group >= len(r.groups) {
		return provider.Result{}, false
	}
	g := &r.groups[it.group]
	if len(g.rowIdx) == 0 {
		return provider.Result{}, false
	}
	leaf := it.leaf
	if leaf < 0 {
		leaf = 0
	}
	if leaf >= len(g.rowIdx) {
		return provider.Result{}, false
	}
	idx := g.rowIdx[leaf]
	if idx < 0 || idx >= len(r.rows) {
		return provider.Result{}, false
	}
	return r.rows[idx].res, true
}

func (a *App) viewGraph(width, h int) string {
	r := &a.results
	items := r.navItems()
	return renderWindow(&r.gwin, len(items), h, width, func(i int, selected bool) string {
		it := items[i]
		g := &r.groups[it.group]
		switch {
		case len(g.rowIdx) == 1:
			return r.graphFlatLeaf(g.rowIdx[0], width, selected)
		case it.leaf == -1:
			return r.graphHeader(g, width, selected)
		default:
			return r.graphLeaf(g, it.leaf, width, selected)
		}
	})
}

// graphColumns is the faint column-label row above the grouped view, aligned to
// the same grid as every row (matching the flat view's header line).
func (a *App) graphColumns(width int) string {
	lay := newGraphLayout(width)
	head := fmt.Sprintf("   %s %s",
		padRight("title", lay.titleW),
		lay.cols("src", "provider", "seeds", "", "size"),
	)
	return styleFaint.Render(truncate(head, width))
}

// titleCell fits a title plus its trailing badges into the flexible column:
// badges keep their full width and the title gives way.
func titleCell(title string, w int, badges string) string {
	if badges == "" {
		return truncate(title, w)
	}
	avail := max(1, w-lipglossWidth(badges)-1)
	return truncate(title, avail) + " " + badges
}

// graphHeader is the decision row for a group: the title flexes (carrying the
// gold "best" badge when its top source is the single best pick overall), then
// the same fixed columns every row shares - source count, provider, seeders,
// the swarm meter, and size, all summarizing the group's champion.
func (r *resultsModel) graphHeader(g *group, width int, selected bool) string {
	lay := newGraphLayout(width)
	allNoisy := groupAllNoisy(r, g)
	plain := selected || allNoisy // render flat; the selection/dim wrapper carries emphasis
	best, ok := r.bestGroupRow(g)
	if !ok {
		return ""
	}
	arrow := "▾"
	if g.collapsed {
		arrow = "▸"
	}
	arrowS := arrow
	if !plain {
		arrowS = styleFaint.Render(arrow)
	}
	badge := ""
	titleW := lay.titleW
	if g.rowIdx[0] == r.bestIdx {
		badge = colorize(plain, styleBest, "best")
		titleW = max(1, lay.titleW-lipglossWidth(badge)-1)
	}
	label := truncate(g.label, titleW)
	if !plain {
		label = styleFg.Render(label) // the group's own voice, brighter than its sources
	}
	if badge != "" {
		label += " " + badge
	}
	size := truncate(best.res.Size, lay.sizeW)
	if size == "" {
		size = "?"
	}
	cols := lay.cols(
		fmt.Sprintf("×%d", len(g.rowIdx)),
		providerCol(best.res.Provider, plain),
		seedPill(best.res.Seeders, plain),
		seederMeter(best.res.Seeders, r.meterMax, lay.meterW, plain),
		size,
	)
	line := fmt.Sprintf("%s %s %s", arrowS, padRight(label, lay.titleW), cols)
	line = truncate(line, width-1) // renderWindow prepends the 1-col gutter
	if !selected && allNoisy {
		return styleFaint.Render(line)
	}
	return line
}

// graphLeaf renders one source inside an expanded group as a numbered choice
// on a faint tree guide, followed by the release fingerprint - the parts that
// actually differ between sources of the same content (source, codec, HDR/DV,
// release group) - so the group can be compared without leaving the list. The
// "#1" already marks the in-group best, so leaves never carry the "best"
// badge; that stays reserved for the single overall pick on its header/flat
// row.
func (r *resultsModel) graphLeaf(g *group, leaf, width int, selected bool) string {
	lay := newGraphLayout(width)
	row := r.rows[g.rowIdx[leaf]]
	plain := selected || row.noisy

	guide := "├"
	if leaf == len(g.rowIdx)-1 {
		guide = "└"
	}
	guideS := guide
	if !plain {
		guideS = styleFaint.Render(guide)
	}
	inner := fmt.Sprintf("#%d", leaf+1)
	if fp := leafFingerprint(row); fp != "" {
		inner += "  " + colorize(plain, styleDim, fp)
	}
	if b := sourceBadges(row, plain); b != "" {
		inner = titleCell(inner, lay.titleW, b)
	}
	cols := lay.cols(
		"",
		providerCol(row.res.Provider, plain),
		seedPill(row.res.Seeders, plain),
		seederMeter(row.res.Seeders, r.meterMax, lay.meterW, plain),
		truncate(row.res.Size, lay.sizeW),
	)
	line := fmt.Sprintf("%s %s %s", guideS, padRight(truncate(inner, lay.titleW), lay.titleW), cols)
	line = truncate(line, width-1) // renderWindow prepends the 1-col gutter
	if !selected && row.noisy {
		return styleFaint.Render(line)
	}
	return line
}

// leafFingerprint is the compact release identity of one source: source,
// codec, HDR/DV, and the scene group. It intentionally skips resolution and
// season - the group label already fixes those.
func leafFingerprint(row scoredRow) string {
	var parts []string
	if s := row.tags.Source.String(); s != "" {
		parts = append(parts, s)
	}
	if c := row.tags.Codec; c != "" {
		parts = append(parts, c)
	}
	switch {
	case row.tags.DV && row.tags.HDR:
		parts = append(parts, "DV·HDR")
	case row.tags.DV:
		parts = append(parts, "DV")
	case row.tags.HDR:
		parts = append(parts, "HDR")
	}
	if g := rank.ReleaseGroup(row.res.Title); g != "" {
		parts = append(parts, "-"+g)
	}
	return strings.Join(parts, " ")
}

// graphFlatLeaf renders a single-source group as a dense one-liner: the title
// flexes (with the gold "best" badge and any status badges trailing it), then
// the same fixed columns as the headers. A blank arrow slot keeps its title
// aligned with group labels.
func (r *resultsModel) graphFlatLeaf(idx, width int, selected bool) string {
	lay := newGraphLayout(width)
	row := r.rows[idx]
	plain := selected || row.noisy

	badges := sourceBadges(row, plain)
	if idx == r.bestIdx {
		b := colorize(plain, styleBest, "best")
		if badges != "" {
			b += " " + badges
		}
		badges = b
	}
	title := titleCell(row.res.Title, lay.titleW, badges)
	cols := lay.cols(
		"",
		providerCol(row.res.Provider, plain),
		seedPill(row.res.Seeders, plain),
		seederMeter(row.res.Seeders, r.meterMax, lay.meterW, plain),
		truncate(row.res.Size, lay.sizeW),
	)
	line := fmt.Sprintf("  %s %s", padRight(title, lay.titleW), cols)
	line = truncate(line, width-1) // renderWindow prepends the 1-col gutter
	if !selected && row.noisy {
		return styleFaint.Render(line)
	}
	return line
}

// sourceBadges is the trailing status text (trusted / resolve / noisy). It stays
// ASCII so terminal fonts do not turn the graph into emoji soup. The gold "best"
// badge is added by the row renderers, not here, since only one row earns it.
func sourceBadges(row scoredRow, plain bool) string {
	res := row.res
	var badges []string
	if res.Trusted {
		badges = append(badges, colorize(plain, styleOK, "trusted"))
	}
	if res.Magnet == "" {
		badges = append(badges, colorize(plain, styleFaint, "resolve"))
	}
	if row.noisy {
		badges = append(badges, colorize(plain, styleFaint, "noisy"))
	}
	return strings.Join(badges, " ")
}

// seedPill renders S<n> colored by swarm health (faint when dead); plain drops
// the color so the selection or dim wrapper carries it.
func seedPill(seeders int, plain bool) string {
	s := fmt.Sprintf("S%d", seeders)
	if plain {
		return s
	}
	st := seederBarStyle(seeders)
	if seeders <= 0 {
		st = styleFaint
	}
	return st.Render(s)
}

// providerCol is a bracketed provider tag, uncolored when plain.
func providerCol(name string, plain bool) string {
	if plain {
		return "[" + name + "]"
	}
	return providerBracket(name)
}

// colorize renders s with st, or plain when the row wrapper owns emphasis.
func colorize(plain bool, st lipgloss.Style, s string) string {
	if plain {
		return s
	}
	return st.Render(s)
}

func (r *resultsModel) bestGroupRow(g *group) (scoredRow, bool) {
	if g == nil || len(g.rowIdx) == 0 {
		return scoredRow{}, false
	}
	idx := g.rowIdx[0]
	if idx < 0 || idx >= len(r.rows) {
		return scoredRow{}, false
	}
	return r.rows[idx], true
}

// groupProviders lists a group's distinct providers as bracketed tags, capped
// at three with a faint "+N" for the rest.
func groupProviders(r *resultsModel, g *group, plain bool) string {
	seen := map[string]bool{}
	var names []string
	for _, idx := range g.rowIdx {
		p := r.rows[idx].res.Provider
		if !seen[p] {
			seen[p] = true
			names = append(names, p)
		}
	}
	const cap = 3
	extra := 0
	if len(names) > cap {
		extra = len(names) - cap
		names = names[:cap]
	}
	tags := make([]string, len(names))
	for i, p := range names {
		tags[i] = providerCol(p, plain)
	}
	out := strings.Join(tags, " ")
	if extra > 0 {
		out += " " + colorize(plain, styleFaint, fmt.Sprintf("+%d", extra))
	}
	return out
}

// groupAllNoisy reports whether every source in a group is noisy, so the whole
// header can be dimmed.
func groupAllNoisy(r *resultsModel, g *group) bool {
	for _, idx := range g.rowIdx {
		if !r.rows[idx].noisy {
			return false
		}
	}
	return len(g.rowIdx) > 0
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
		best, _ := r.bestGroupRow(g)
		median, spread := groupSizeSpread(r.rows, g.rowIdx)
		lines = append(lines,
			styleFg.Render(truncate(g.label, width)),
			fmt.Sprintf("%s %s %s  %s", styleBest.Render("best"), providerBracket(best.res.Provider), seedPill(best.res.Seeders, false), styleDim.Render(truncate(best.res.Size, 12))),
			fmt.Sprintf("%s  %s  %s", styleDim.Render(fmt.Sprintf("%d providers", groupProviderCount(r, g))), groupProviders(r, g, false), styleDim.Render(fmt.Sprintf("total S%d", groupTotalSeeders(r, g)))),
			formatSizeSpread(median, spread)+styleFaint.Render(" · ")+groupWarnings(r, g),
		)
		return strings.Join(lines, "\n")
	}
	row := r.rows[g.rowIdx[it.leaf]]
	median, spread := groupSizeSpread(r.rows, g.rowIdx)
	magnet := styleOK.Render("magnet ready")
	if row.res.Magnet == "" {
		magnet = styleFaint.Render("needs resolve")
	}
	trusted := styleFaint.Render("untrusted")
	if row.res.Trusted {
		trusted = styleOK.Render("trusted")
	}
	noise := "noise: none"
	if reasons := rank.NoiseReasons(r.query, row.res.Title, row.tags, row.res.Seeders); len(reasons) > 0 {
		noise = "noise: " + strings.Join(reasons, ", ")
	}
	pos := fmt.Sprintf("#%d of %d", it.leaf+1, len(g.rowIdx))
	if fp := leafFingerprint(row); fp != "" {
		pos += "  " + fp
	}
	lines = append(lines,
		styleFg.Render(truncate(row.res.Title, width)),
		fmt.Sprintf("%s  %s  %s  %s", providerBracket(row.res.Provider), trusted, magnet, styleDim.Render(pos)),
		fmt.Sprintf("%s %d   %s %d   %s", styleSeeders.Render("S"), row.res.Seeders, styleLeechers.Render("L"), row.res.Leechers, styleDim.Render(row.res.Size)),
		formatSizeSpread(median, spread)+styleFaint.Render(" · ")+styleDim.Render(noise),
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

func groupProviderCount(r *resultsModel, g *group) int {
	seen := map[string]bool{}
	for _, idx := range g.rowIdx {
		seen[r.rows[idx].res.Provider] = true
	}
	return len(seen)
}

func groupWarnings(r *resultsModel, g *group) string {
	noisy, unresolved := 0, 0
	for _, idx := range g.rowIdx {
		row := r.rows[idx]
		if row.noisy {
			noisy++
		}
		if row.res.Magnet == "" {
			unresolved++
		}
	}
	if noisy == 0 && unresolved == 0 {
		return styleOK.Render("warnings none")
	}
	var parts []string
	if noisy > 0 {
		parts = append(parts, fmt.Sprintf("%d noisy", noisy))
	}
	if unresolved > 0 {
		parts = append(parts, fmt.Sprintf("%d resolve", unresolved))
	}
	return styleHealthMid.Render("warnings " + strings.Join(parts, ", "))
}

// graphBarCells sizes the in-group seed meter: a tiny fixed strip, dropped
// entirely on narrow terminals.
func graphBarCells(width int) int {
	if width < 60 {
		return 0
	}
	return 5
}

// seederBar is the colored meter (kept for callers/tests that don't need the
// plain variant).
func seederBar(seeders, maxSeeders, cells int) string {
	return seederMeter(seeders, maxSeeders, cells, false)
}

// seederMeter renders a log-scaled filled/empty strip. When plain, it carries
// no color so the row's selection/dim wrapper owns the emphasis.
func seederMeter(seeders, maxSeeders, cells int, plain bool) string {
	if cells < 3 {
		return ""
	}
	filled := 0
	if maxSeeders > 0 && seeders > 0 {
		filled = int(math.Round(float64(cells) * math.Log2(float64(1+seeders)) / math.Log2(float64(1+maxSeeders))))
		filled = max(1, min(cells, filled))
	}
	bars := strings.Repeat("▮", filled)
	empty := strings.Repeat("▯", cells-filled)
	if plain {
		return bars + empty
	}
	if filled == 0 {
		return styleFaint.Render(empty)
	}
	return seederBarStyle(seeders).Render(bars) + styleFaint.Render(empty)
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
