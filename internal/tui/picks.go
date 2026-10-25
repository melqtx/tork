package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/melqtx/tork/internal/rank"
)

var resolutionOrder = []rank.Resolution{rank.Res1080, rank.Res2160, rank.Res720, rank.Res576, rank.Res480, rank.ResUnknown}

func resolutionName(r rank.Resolution) string {
	if r == rank.ResUnknown {
		return "Other / unspecified"
	}
	return r.String()
}

type pickItem struct {
	row        int
	resolution rank.Resolution
	label, key string
}
type pickPreferences struct {
	resolutions uint8
	size        int
	preference  int
}

var pickSizes = []int64{0, 2 << 30, 5 << 30, 10 << 30, 20 << 30, 40 << 30}
var pickModes = []string{"Balanced", "Smaller files", "Higher quality"}

func (p pickPreferences) allows(row scoredRow) bool {
	return (p.resolutions == 0 || p.resolutions&(1<<uint(row.tags.Resolution)) != 0) && (pickSizes[p.size] == 0 || row.res.SizeBytes > 0 && row.res.SizeBytes <= pickSizes[p.size])
}

// Relevance is compared before swarm size: a popular wrong title or episode
// must not displace a match. Uncertain matches remain available in the group.
func queryMatch(query string, row scoredRow) int {
	q, year := rank.SplitTitle(query)
	title, gotYear := rank.SplitTitle(row.res.Title)
	score := 0
	if q != "" {
		if q == title {
			score = 100
		} else {
			words := strings.Fields(q)
			hits := 0
			for _, w := range words {
				if strings.Contains(" "+title+" ", " "+w+" ") {
					hits++
				}
			}
			if len(words) > 0 {
				score = 60 * hits / len(words)
			}
		}
	}
	if year != "" {
		if year == gotYear {
			score += 40
		} else {
			score -= 80
		}
	}
	t := rank.Parse(query)
	if t.Season > 0 {
		if row.tags.Season == t.Season || row.tags.Season <= t.Season && row.tags.SeasonEnd >= t.Season {
			score += 40
		} else {
			score -= 80
		}
	}
	if t.Episode > 0 {
		if row.tags.Episode == t.Episode {
			score += 40
		} else {
			score -= 80
		}
	}
	return score
}
func (r *resultsModel) pickBetter(i, j int) bool {
	return r.pickRowsBetter(r.rows[i], r.rows[j])
}
func (r *resultsModel) pickRowsBetter(a, b scoredRow) bool {
	am, bm := queryMatch(r.query, a), queryMatch(r.query, b)
	if am != bm {
		return am > bm
	}
	// Do not assume an unrequested language is undesirable.
	usable := func(x scoredRow) bool { return x.res.Seeders > 0 && x.tags.Source != rank.SrcCam }
	if usable(a) != usable(b) {
		return usable(a)
	}
	switch r.pickPrefs.preference {
	case 1:
		if (a.res.SizeBytes > 0) != (b.res.SizeBytes > 0) {
			return a.res.SizeBytes > 0
		}
		if a.res.SizeBytes != b.res.SizeBytes {
			return a.res.SizeBytes < b.res.SizeBytes
		}
	case 2:
		if a.tags.Source != b.tags.Source {
			return a.tags.Source > b.tags.Source
		}
	}
	if a.score != b.score {
		return a.score > b.score
	}
	return rowIdentity(a) < rowIdentity(b)
}

// Resolution groups help video searches, but hide useful games, software and
// books behind "Other". Use one ranked list when most results lack resolution.
func (r *resultsModel) resolutionPicks() bool {
	known := 0
	for _, idx := range r.visible {
		if r.rows[idx].tags.Resolution != rank.ResUnknown {
			known++
		}
	}
	return known > 0 && known*2 >= len(r.visible)
}

func (r *resultsModel) rebuildPicks() {
	oldKey := r.pickKey
	if !r.pickPinned {
		oldKey = ""
	}
	oldOffset := r.pickWin.offset
	grouped := r.resolutionPicks()
	groups := map[rank.Resolution][]int{}
	for _, idx := range r.visible {
		res := rank.ResUnknown
		if grouped {
			res = r.rows[idx].tags.Resolution
		}
		groups[res] = append(groups[res], idx)
	}
	r.picks = nil
	for _, res := range resolutionOrder {
		indices := groups[res]
		if len(indices) == 0 {
			continue
		}
		sort.SliceStable(indices, func(i, j int) bool { return r.pickBetter(indices[i], indices[j]) })
		if grouped {
			r.picks = append(r.picks, pickItem{row: -1, resolution: res, label: fmt.Sprintf("%s · %s", resolutionName(res), plural(len(indices), "release"))})
		}
		limit := len(indices)
		if grouped && !r.pickExpanded[res] && limit > 3 {
			limit = 3
		}
		shown := append([]int(nil), indices[:limit]...)
		if r.pickPinned && oldKey != "" && limit < len(indices) {
			for _, idx := range indices[limit:] {
				if rowIdentity(r.rows[idx]) == oldKey {
					shown[len(shown)-1] = idx
					break
				}
			}
		}
		for _, idx := range shown {
			r.picks = append(r.picks, pickItem{row: idx, resolution: res, key: rowIdentity(r.rows[idx])})
		}
		if grouped && len(indices) > 3 {
			label := fmt.Sprintf("Show %d more releases", len(indices)-3)
			if r.pickExpanded[res] {
				label = "Show fewer releases"
			}
			r.picks = append(r.picks, pickItem{row: -2, resolution: res, label: label, key: "more:" + resolutionName(res)})
		}
	}
	r.pickWin.cursor = 0
	if oldKey != "" {
		for i, it := range r.picks {
			if it.key == oldKey {
				r.pickWin.cursor = i
				break
			}
		}
	}
	r.movePick(0, max(1, len(r.picks)))
	r.pickWin.offset = oldOffset
}
func (r *resultsModel) movePick(delta, height int) {
	r.pickWin.move(delta, len(r.picks), height)
	direction := 1
	if delta < 0 {
		direction = -1
	}
	for r.pickWin.cursor >= 0 && r.pickWin.cursor < len(r.picks) && r.picks[r.pickWin.cursor].row == -1 {
		next := r.pickWin.cursor + direction
		if next < 0 || next >= len(r.picks) {
			direction = -direction
			next = r.pickWin.cursor + direction
		}
		r.pickWin.cursor = next
	}
	r.pickWin.clamp(len(r.picks), height)
	if r.pickWin.cursor < len(r.picks) {
		r.pickKey = r.picks[r.pickWin.cursor].key
	}
}
func (a *App) pickRows() int {
	if a.bodyHeight() < 10 {
		return max(1, a.bodyHeight()-3)
	}
	return max(1, a.bodyHeight()-7)
}
func (a *App) updatePickKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	switch msg.String() {
	case "up", "k", "down", "j", "pgup", "pgdown", "g", "home", "G", "end":
		r.pickPinned = true
	}
	switch msg.String() {
	case "up", "k":
		r.movePick(-1, a.pickRows())
	case "down", "j":
		r.movePick(1, a.pickRows())
	case "pgup":
		r.movePick(-a.pickRows(), a.pickRows())
	case "pgdown":
		r.movePick(a.pickRows(), a.pickRows())
	case "g", "home":
		r.pickWin.home()
		r.movePick(0, a.pickRows())
	case "G", "end":
		r.pickWin.end(len(r.picks), a.pickRows())
		r.movePick(0, a.pickRows())
	case "enter", "D", "Y":
		if r.pickWin.cursor >= len(r.picks) {
			return a, nil
		}
		it := r.picks[r.pickWin.cursor]
		if it.row == -2 {
			if r.pickExpanded == nil {
				r.pickExpanded = map[rank.Resolution]bool{}
			}
			r.pickExpanded[it.resolution] = !r.pickExpanded[it.resolution]
			r.rebuildPicks()
			return a, nil
		}
		if it.row >= 0 {
			res := r.rows[it.row].res
			switch msg.String() {
			case "D":
				return a, a.downloadResultDirect(res)
			case "Y":
				return a, a.yankResult(res)
			}
			return a, a.downloadResult(res)
		}
	}
	return a, nil
}
func (a *App) viewPicks() string {
	r := &a.results
	w := a.contentWidth()
	summary := pickModes[r.pickPrefs.preference] + " · ranked by relevance"
	if r.resolutionPicks() {
		summary = pickModes[r.pickPrefs.preference] + " · top 3 per resolution"
	}
	if r.pickPrefs.resolutions != 0 {
		var names []string
		for _, res := range resolutionOrder {
			if r.pickPrefs.resolutions&(1<<uint(res)) != 0 {
				names = append(names, resolutionName(res))
			}
		}
		summary = strings.Join(names, " + ") + " · " + pickModes[r.pickPrefs.preference]
	}
	if pickSizes[r.pickPrefs.size] > 0 {
		summary += " · ≤ " + humanBytes(pickSizes[r.pickPrefs.size])
	}
	if r.filterIn.Value() != "" {
		summary += " · text: " + r.filterIn.Value()
	}
	if w < 76 {
		summary = pickModes[r.pickPrefs.preference]
		if r.pickPrefs.resolutions != 0 || r.pickPrefs.size != 0 || r.filterIn.Value() != "" {
			summary += " · filtered"
		}
	}
	if w < 40 {
		summary = ""
	}
	count := fmt.Sprintf("%d results", len(r.rows))
	if len(r.visible) != len(r.rows) {
		count = fmt.Sprintf("%d/%d results", len(r.visible), len(r.rows))
	}
	if r.searching {
		count += " · live"
	}
	left := styleBrand.Render(count)
	if r.merged > 0 {
		left += styleFaint.Render(fmt.Sprintf(" · %d merged", r.merged))
	}
	hidden := 0
	for _, ev := range r.status {
		hidden += ev.Hidden
	}
	if hidden > 0 {
		left += styleFaint.Render(fmt.Sprintf(" · %d hidden", hidden))
	}
	body := splitResultLine(left, styleDim.Render(summary), w) + "\n" + a.resultSourceSummary(w) + "\n"
	if len(r.picks) == 0 {
		label := "No matches with these filters. Press / to adjust or reset."
		if r.searching {
			label = "Searching… picks appear as sources respond."
		} else if len(r.rows) == 0 {
			label = "No results. Try another search, or press s to check sources."
		}
		if r.query == "" {
			label = "Type a search on home to get started."
		}
		return a.chrome("results · "+r.query, body+"\n"+styleDim.Render(label), a.keyStrip(a.helpBudget(w)))
	}
	body += " " + releaseHeader(w-1) + "\n"
	body += renderWindow(&r.pickWin, len(r.picks), a.pickRows(), w, func(i int, selected bool) string {
		it := r.picks[i]
		if it.row == -1 {
			return styleTitle.Render(it.label)
		}
		if it.row == -2 {
			return styleDim.Render("  " + it.label)
		}
		return renderReleaseRow(r.rows[it.row], w-1)
	})
	if a.bodyHeight() < 10 {
		return a.chrome("results · "+r.query, body, a.keyStrip(a.helpBudget(w)))
	}
	it := r.picks[r.pickWin.cursor]
	if it.row >= 0 {
		row := r.rows[it.row]
		reason := "ranked by title match, source quality and reported swarm"
		if row.res.Seeders <= 0 {
			reason = "no seeders reported · availability uncertain"
		} else if row.tags.Source == rank.SrcCam {
			reason = "CAM source · lower image quality"
		} else if r.pickPrefs.preference == 1 {
			reason = "smaller known files first, after title match and availability"
		} else if r.pickPrefs.preference == 2 {
			reason = "higher source quality first, after title match and availability"
		}
		q, qYear := rank.SplitTitle(r.query)
		title, titleYear := rank.SplitTitle(row.res.Title)
		if q != "" && q != title {
			reason = "partial title match · " + reason
		}
		if qYear != "" && qYear != titleYear {
			reason = "year differs or unlisted · " + reason
		}
		label := "Selected · " + sourceTag(row.res, true)
		body += "\n" + detailRule(label, w) + "\n" + styleFg.Render(row.res.Title) + "\n"
		facts := releaseQuality(row)
		if row.res.SizeBytes > 0 {
			facts += " · " + humanBytes(row.res.SizeBytes)
		}
		facts += fmt.Sprintf(" · %d seeds", max(0, row.res.Seeders))
		body += styleDim.Render(facts) + "\n" + styleDim.Render(reason)
	} else {
		body += "\n" + detailRule("More releases", w) + "\n" + styleDim.Render("enter "+strings.ToLower(it.label))
	}
	return a.chrome("results · "+r.query, body, a.keyStrip(a.helpBudget(w)))
}

func detailRule(label string, width int) string {
	label = truncate(label, width)
	line := styleKey.Render(label)
	if remaining := width - lipgloss.Width(label) - 2; remaining > 0 {
		line += "  " + rule(remaining)
	}
	return truncate(line, width)
}
