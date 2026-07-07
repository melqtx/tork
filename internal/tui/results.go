package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

// scoredRow is a search result with its parsed tags and computed score cached.
type scoredRow struct {
	res   provider.Result
	tags  rank.Tags
	score float64
}

type sortMode int

const (
	sortScore sortMode = iota
	sortSeeders
	sortSize
)

func (s sortMode) String() string {
	switch s {
	case sortSeeders:
		return "seeders"
	case sortSize:
		return "size"
	}
	return "score"
}

type resultsModel struct {
	query     string
	rows      []scoredRow     // sorted by the active sort mode
	seen      map[string]bool // dedupe across providers/retries
	visible   []int           // indices into rows after fuzzy filter
	matched   map[int][]int   // row index -> matched rune positions in title
	cursor    int             // index into visible (flat mode)
	offset    int             // scroll offset
	filtering bool
	filterIn  textinput.Model
	status    map[string]aggregator.StatusEvent
	searching bool
	resolving bool
	weights   rank.Weights
	sort      sortMode

	grouped bool // source-graph view toggle (see graphview.go)
	groups  []group
	gcursor int // cursor over the flattened grouped view

	resultCh <-chan provider.Result
	statusCh <-chan aggregator.StatusEvent
	cancel   context.CancelFunc

	openResults bool // channel-closed bookkeeping
	openStatus  bool
}

func newResultsModel(w rank.Weights) resultsModel {
	fi := textinput.New()
	fi.Prompt = "/"
	fi.CharLimit = 100
	return resultsModel{
		seen:        make(map[string]bool),
		matched:     make(map[int][]int),
		status:      make(map[string]aggregator.StatusEvent),
		filterIn:    fi,
		weights:     w,
		openResults: true,
		openStatus:  true,
	}
}

// betterThan reports whether row a should rank above row b under the active
// sort mode (all modes are descending, with score as the tie-breaker).
func (r *resultsModel) betterThan(a, b scoredRow) bool {
	switch r.sort {
	case sortSeeders:
		if a.res.Seeders != b.res.Seeders {
			return a.res.Seeders > b.res.Seeders
		}
	case sortSize:
		if a.res.SizeBytes != b.res.SizeBytes {
			return a.res.SizeBytes > b.res.SizeBytes
		}
	default:
		if a.score != b.score {
			return a.score > b.score
		}
	}
	return a.score > b.score
}

func (a *App) updateResults(msg tea.Msg) (tea.Model, tea.Cmd) {
	r := &a.results
	switch msg := msg.(type) {
	case resultMsg:
		r.insert(msg.r)
		return a, waitForResult(r.resultCh)

	case statusMsg:
		r.status[msg.ev.Provider] = msg.ev
		return a, waitForStatus(r.statusCh)

	case resultsClosedMsg:
		r.openResults = false
		r.searching = r.openStatus
		return a, nil

	case statusClosedMsg:
		r.openStatus = false
		r.searching = r.openResults
		return a, nil

	case magnetResolvedMsg:
		r.resolving = false
		if msg.err != nil {
			a.errText = "resolve failed: " + msg.err.Error()
			return a, clearErrCmd()
		}
		return a, a.launchCmd(msg.magnet, msg.res.Title, msg.preview)

	case tea.KeyMsg:
		if r.filtering {
			return a.updateResultsFilter(msg)
		}
		return a.updateResultsKeys(msg)
	}
	return a, nil
}

func (a *App) updateResultsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	// keys common to both flat and grouped views
	switch msg.String() {
	case "q":
		return a, tea.Quit
	case "esc":
		a.screen = screenSearch
		return a, a.search.input.Focus()
	case "/":
		r.filtering = true
		return a, r.filterIn.Focus()
	case "o":
		r.sort = (r.sort + 1) % 3
		r.resort()
		return a, nil
	case "v":
		r.grouped = !r.grouped
		if r.grouped {
			r.gcursor = 0
			r.rebuildGroups()
		}
		return a, nil
	}
	if r.grouped {
		return a.updateGraphKeys(msg)
	}

	switch msg.String() {
	case "up", "k":
		r.move(-1)
	case "down", "j":
		r.move(1)
	case "pgup":
		r.move(-a.listRows())
	case "pgdown":
		r.move(a.listRows())
	case "g", "home":
		r.cursor = 0
		r.clampScroll(a.listRows())
	case "G", "end":
		r.cursor = max(0, len(r.visible)-1)
		r.clampScroll(a.listRows())
	case "enter":
		return a, a.selectResult()
	case "D":
		if r.cursor >= 0 && r.cursor < len(r.visible) {
			return a, a.downloadResultDirect(r.rows[r.visible[r.cursor]].res)
		}
	}
	return a, nil
}

func (a *App) updateResultsFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	switch msg.String() {
	case "esc":
		r.filtering = false
		r.filterIn.SetValue("")
		r.filterIn.Blur()
		r.refreshFilter()
		return a, nil
	case "enter":
		r.filtering = false
		r.filterIn.Blur()
		return a, nil
	}
	var cmd tea.Cmd
	r.filterIn, cmd = r.filterIn.Update(msg)
	r.refreshFilter()
	return a, cmd
}

// selectResult downloads the row under the flat-view cursor.
func (a *App) selectResult() tea.Cmd {
	r := &a.results
	if r.cursor < 0 || r.cursor >= len(r.visible) {
		return nil
	}
	return a.downloadResult(r.rows[r.visible[r.cursor]].res)
}

// downloadResult acts on a row: opens the preview sandbox when configured,
// otherwise downloads directly. Shared by the flat list and the graph view.
func (a *App) downloadResult(res provider.Result) tea.Cmd {
	return a.actOnResult(res, a.cfg.PreviewBeforeDownload)
}

// downloadResultDirect always skips the preview (the `D` shortcut).
func (a *App) downloadResultDirect(res provider.Result) tea.Cmd {
	return a.actOnResult(res, false)
}

func (a *App) actOnResult(res provider.Result, preview bool) tea.Cmd {
	r := &a.results
	if r.resolving {
		return nil
	}
	if res.Magnet != "" {
		return a.launchCmd(res.Magnet, res.Title, preview)
	}
	resolver := a.findResolver(res.Provider)
	if resolver == nil {
		a.errText = res.Provider + ": cannot resolve magnet"
		return clearErrCmd()
	}
	r.resolving = true
	return func() (msg tea.Msg) {
		defer guard(&msg, func(r any) tea.Msg {
			return magnetResolvedMsg{res: res, err: fmt.Errorf("resolve panicked: %v", r)}
		})
		ctx, cancel := context.WithTimeout(context.Background(), a.cfg.SearchTimeout())
		defer cancel()
		magnet, err := resolver.ResolveMagnet(ctx, res)
		return magnetResolvedMsg{res: res, magnet: magnet, preview: preview, err: err}
	}
}

// launchCmd either enters the preview screen or downloads immediately.
func (a *App) launchCmd(magnet, name string, preview bool) tea.Cmd {
	if preview {
		return func() (msg tea.Msg) {
			defer guard(&msg, func(r any) tea.Msg {
				return previewReadyMsg{magnet: magnet, name: name, err: fmt.Errorf("add panicked: %v", r)}
			})
			h, err := a.eng.AddForPreview(magnet)
			return previewReadyMsg{hash: h, magnet: magnet, name: name, err: err}
		}
	}
	return a.addTorrentCmd(magnet, name)
}

func (a *App) findResolver(name string) provider.MagnetResolver {
	for _, p := range a.agg.Providers() {
		if p.Name() == name {
			if mr, ok := p.(provider.MagnetResolver); ok {
				return mr
			}
		}
	}
	return nil
}

func (a *App) addTorrentCmd(magnet, name string) tea.Cmd {
	return func() (msg tea.Msg) {
		defer guard(&msg, func(r any) tea.Msg {
			return torrentAddedMsg{magnet: magnet, name: name, err: fmt.Errorf("add panicked: %v", r)}
		})
		h, err := a.eng.Add(magnet, nil)
		return torrentAddedMsg{hash: h, magnet: magnet, name: name, err: err}
	}
}

// insert adds a result keeping rows sorted by the active mode, skipping dupes.
func (r *resultsModel) insert(res provider.Result) {
	if r.seen[res.Key()] {
		return
	}
	r.seen[res.Key()] = true
	tags := rank.Parse(res.Title)
	row := scoredRow{res: res, tags: tags, score: rank.Score(res, tags, r.weights)}
	// first position where the existing row is not better than the new one
	pos := sort.Search(len(r.rows), func(i int) bool { return !r.betterThan(r.rows[i], row) })
	r.rows = append(r.rows, scoredRow{})
	copy(r.rows[pos+1:], r.rows[pos:])
	r.rows[pos] = row
	r.refreshFilter()
}

// resort re-orders all rows after a sort-mode change and rebuilds the view.
func (r *resultsModel) resort() {
	sort.SliceStable(r.rows, func(i, j int) bool { return r.betterThan(r.rows[i], r.rows[j]) })
	r.refreshFilter()
}

// refreshFilter recomputes visible rows and match highlights.
func (r *resultsModel) refreshFilter() {
	term := strings.TrimSpace(r.filterIn.Value())
	r.matched = make(map[int][]int)
	if term == "" {
		r.visible = r.visible[:0]
		for i := range r.rows {
			r.visible = append(r.visible, i)
		}
	} else {
		titles := make([]string, len(r.rows))
		for i, row := range r.rows {
			titles[i] = row.res.Title
		}
		matches := fuzzy.Find(term, titles)
		r.visible = r.visible[:0]
		for _, m := range matches {
			r.visible = append(r.visible, m.Index)
			r.matched[m.Index] = m.MatchedIndexes
		}
	}
	if r.cursor >= len(r.visible) {
		r.cursor = max(0, len(r.visible)-1)
	}
	if r.grouped {
		r.rebuildGroups()
	}
}

func (r *resultsModel) move(delta int) {
	r.cursor = max(0, min(len(r.visible)-1, r.cursor+delta))
}

// listRows is the number of visible result rows (body minus status + columns).
func (a *App) listRows() int {
	return max(1, a.bodyHeight()-2)
}

// clampScroll keeps the cursor within the visible window of h rows.
func (r *resultsModel) clampScroll(h int) {
	if h < 1 {
		h = 1
	}
	if r.cursor < r.offset {
		r.offset = r.cursor
	}
	if r.cursor >= r.offset+h {
		r.offset = r.cursor - h + 1
	}
	r.offset = max(0, min(r.offset, max(0, len(r.visible)-h)))
}

func (a *App) viewResults() string {
	r := &a.results
	width := a.contentWidth()
	listH := a.listRows()

	var b strings.Builder
	b.WriteString(r.statusLine(a.agg) + "\n")

	if r.grouped {
		b.WriteString(a.viewGraph(width, listH))
	} else {
		// title (flex) · dot(2) · size 11 · S 5 · L 5 · res 5 · prov
		titleW := max(20, width-40)
		b.WriteString(styleFaint.Render(fmt.Sprintf("   %-*s %11s %5s %5s %5s  %s", titleW, "title", "size", "S", "L", "res", "prov")) + "\n")

		r.clampScroll(listH)
		end := min(len(r.visible), r.offset+listH)
		for vi := r.offset; vi < end; vi++ {
			idx := r.visible[vi]
			row := r.rows[idx]
			dead := row.res.Seeders <= 0
			resTag := row.tags.Resolution.String()
			line := fmt.Sprintf(" %s %s %11s %s %s %5s  %s",
				healthDot(row.res.Seeders),
				r.renderTitle(idx, titleW),
				truncate(row.res.Size, 11),
				styleSeeders.Render(fmt.Sprintf("%5d", row.res.Seeders)),
				styleLeechers.Render(fmt.Sprintf("%5d", row.res.Leechers)),
				styleFaint.Render(fmt.Sprintf("%5s", resTag)),
				providerTag(row.res.Provider),
			)
			switch {
			case vi == r.cursor:
				line = styleSelected.Render(padRight(line, width))
			case dead:
				line = styleDim.Render(line)
			}
			b.WriteString(line + "\n")
		}
		for i := end - r.offset; i < listH; i++ {
			b.WriteString("\n")
		}
	}

	var help string
	switch {
	case r.filtering:
		help = r.filterIn.View()
	case r.resolving:
		help = styleDim.Render("resolving magnet…")
	case r.grouped:
		help = hints(hint("↑↓", "move"), hint("enter", "open"), hint("←→", "fold"), hint("v", "flat"), hint("/", "filter"), hint("esc", "back"))
	default:
		help = hints(hint("↑↓", "move"), hint("enter", "get"), hint("/", "filter"), hint("o", r.sort.String()), hint("v", "graph"), hint("esc", "back"))
	}

	ctx := "results"
	if r.query != "" {
		ctx = "results · " + r.query
	}
	return a.chrome(ctx, b.String(), help)
}

// renderTitle pads/truncates and highlights fuzzy-matched runes.
func (r *resultsModel) renderTitle(idx, width int) string {
	title := truncate(r.rows[idx].res.Title, width)
	pad := width - len([]rune(title))
	positions := r.matched[idx]
	if len(positions) == 0 {
		return title + strings.Repeat(" ", pad)
	}
	matchSet := make(map[int]bool, len(positions))
	for _, p := range positions {
		matchSet[p] = true
	}
	var b strings.Builder
	for i, ru := range []rune(title) {
		if matchSet[i] {
			b.WriteString(styleMatch.Render(string(ru)))
		} else {
			b.WriteRune(ru)
		}
	}
	b.WriteString(strings.Repeat(" ", pad))
	return b.String()
}

// statusLine leads with a clear result count, then per-provider chips. A
// provider that failed or is simply the wrong category is shown muted, not in
// alarming red - a search "works" as long as any source answered.
func (r *resultsModel) statusLine(agg *aggregator.Aggregator) string {
	chips := make([]string, 0, len(agg.Providers()))
	failed := 0
	for _, p := range agg.Providers() {
		name := p.Name()
		ev, ok := r.status[name]
		var chip string
		switch {
		case !ok, ev.State == aggregator.StateSearching:
			chip = providerTag(name) + " " + styleDim.Render("…")
		case ev.State == aggregator.StateDone && ev.Count > 0:
			chip = providerTag(name) + " " + styleOK.Render(fmt.Sprintf("✓%d", ev.Count))
		case ev.State == aggregator.StateDone:
			chip = styleDim.Render(name + " ·0") // reachable, no matches - not an error
		default:
			reason := "unavailable"
			if ev.Err != nil && strings.Contains(ev.Err.Error(), "blocked") {
				reason = "blocked"
			}
			chip = styleDim.Render(name + " " + reason)
			failed++
		}
		chips = append(chips, chip)
	}

	// leading summary - make success obvious, only alarm on a true zero
	var head string
	switch n := len(r.rows); {
	case n > 0:
		head = styleOK.Render(fmt.Sprintf("%d results", n))
	case r.searching:
		head = styleDim.Render("searching…")
	case failed > 0:
		head = styleErr.Render("no results - sources unavailable, try again")
	default:
		head = styleDim.Render("no results")
	}

	line := head + styleDim.Render("   ") + strings.Join(chips, styleDim.Render(" · "))
	if r.searching {
		line += styleDim.Render("  searching…")
	}
	return line
}
