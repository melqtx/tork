package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

// scoredRow is a search result with its parsed tags and computed score cached.
type scoredRow struct {
	res   provider.Result
	hash  string // cached res.InfoHash(); "" for detail-page rows (see insertRow)
	tags  rank.Tags
	score float64
	noisy bool // display-only: dead/cam/off-topic/language-variant, dimmed in the list
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
	curated         bool
	picks           []pickItem
	pickWin         listWindow
	pickKey         string
	pickPinned      bool
	pickExpanded    map[rank.Resolution]bool
	pickPrefs       pickPreferences
	pickDraft       pickPreferences
	pickPanel       bool
	panelWin        listWindow
	searchID        uint64
	query           string
	rows            []scoredRow     // sorted by the active sort mode
	seen            map[string]bool // dedupe across providers/retries
	hashes          map[string]bool // infohashes already on a row, for cross-provider merging
	merged          int             // duplicate listings folded into an existing row
	visible         []int           // indices into rows after fuzzy filter
	matched         map[int][]int   // row index -> matched rune positions in title
	win             listWindow      // cursor over visible (flat mode)
	selectedKey     string          // stable identity; keeps selection through streamed inserts
	selectionPinned bool            // false keeps following the best row until the user moves
	filtering       bool
	filterIn        textinput.Model
	filterErr       string
	filterHint      string
	filterCommitted bool // Enter switches the live editor to strict validation
	status          map[string]aggregator.StatusEvent
	searching       bool
	resolving       bool
	resolveID       uint64
	resolveCancel   context.CancelFunc
	weights         rank.Weights
	sort            sortMode

	grouped          bool // source-graph view toggle (see graphview.go)
	groups           []group
	gwin             listWindow // cursor over the flattened grouped view
	graphSelectedKey string     // group/result identity; survives regrouping
	bestIdx          int        // r.rows index of the single best pick, or -1 (see recomputeBest)
	meterMax         int        // max seeders among visible rows: one shared meter scale
	resortPending    bool       // a merge changed a row's sort key; refreshFilter re-orders

	resultCh <-chan provider.Result
	statusCh <-chan aggregator.StatusEvent
	cancel   context.CancelFunc

	openResults bool // channel-closed bookkeeping
	openStatus  bool
}

func newResultsModel(w rank.Weights) resultsModel {
	fi := textinput.New()
	fi.Prompt = "/"
	fi.Placeholder = "title · res:1080p · seeders:>20 · size:<8gb"
	fi.CharLimit = 100
	return resultsModel{
		seen:        make(map[string]bool),
		hashes:      make(map[string]bool),
		matched:     make(map[int][]int),
		status:      make(map[string]aggregator.StatusEvent),
		filterIn:    fi,
		weights:     w,
		bestIdx:     -1,
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
		if msg.searchID != r.searchID {
			return a, nil
		}
		// Drain whatever else is already buffered on the channel and refresh the
		// view once, so a burst of providers doesn't trigger a full re-sort +
		// re-filter + re-group per result.
		r.insertRow(msg.r)
		for drained := false; !drained; {
			select {
			case res, ok := <-r.resultCh:
				if !ok {
					r.openResults = false
					r.searching = r.openStatus
					r.refreshFilter()
					return a, nil
				}
				r.insertRow(res)
			default:
				drained = true
			}
		}
		r.refreshFilter()
		return a, waitForResult(r.searchID, r.resultCh)

	case statusMsg:
		if msg.searchID != r.searchID {
			return a, nil
		}
		r.status[msg.ev.Provider] = msg.ev
		return a, waitForStatus(r.searchID, r.statusCh)

	case resultsClosedMsg:
		if msg.searchID != r.searchID {
			return a, nil
		}
		r.openResults = false
		r.searching = r.openStatus
		return a, nil

	case statusClosedMsg:
		if msg.searchID != r.searchID {
			return a, nil
		}
		r.openStatus = false
		r.searching = r.openResults
		return a, nil

	case magnetResolvedMsg:
		if msg.searchID != r.searchID || msg.resolveID != r.resolveID {
			return a, nil
		}
		r.resolving = false
		r.resolveCancel = nil
		if msg.err != nil {
			return a, a.showError("resolve failed: " + msg.err.Error())
		}
		if msg.yank {
			return a, yankDownloadValue("magnet", msg.magnet)
		}
		return a, a.launchCmd(msg.magnet, msg.res.Title, msg.preview)

	case tea.KeyMsg:
		if r.pickPanel {
			return a.updatePickFilters(msg)
		}
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
		a.cancelResolve()
		return a, a.navigate(screenSearch)
	case "/":
		return a, a.openPickFilters()
	case "f":
		r.filtering = true
		r.filterCommitted = false
		return a, r.filterIn.Focus()
	case "o":
		r.sort = (r.sort + 1) % 3
		r.resort()
		return a, nil
	case "v":
		if r.curated {
			r.curated = false
			r.grouped = false
			r.selectedKey = r.pickKey
			r.selectionPinned = true
			r.restoreFlatSelection()
			return a, nil
		}
		r.curated = true
		if r.selectedKey != "" {
			r.pickKey = r.selectedKey
			r.pickPinned = true
		}
		r.rebuildPicks()
		return a, nil
	case "V":
		r.curated = false
		r.grouped = !r.grouped
		if r.grouped {
			r.gwin.home()
			r.graphSelectedKey = ""
			r.rebuildGroups()
		} else {
			// A graph header represents its best source. Returning to flat mode
			// should land on that same choice, not the row selected before the
			// user explored the graph.
			r.selectionPinned = true
			r.restoreFlatSelection()
		}
		return a, nil
	}
	if r.curated {
		return a.updatePickKeys(msg)
	}
	if r.grouped {
		return a.updateGraphKeys(msg)
	}

	switch msg.String() {
	case "up", "k":
		r.selectionPinned = true
		r.win.move(-1, len(r.visible), a.listRows())
	case "down", "j":
		r.selectionPinned = true
		r.win.move(1, len(r.visible), a.listRows())
	case "pgup":
		r.selectionPinned = true
		r.win.move(-a.listRows(), len(r.visible), a.listRows())
	case "pgdown":
		r.selectionPinned = true
		r.win.move(a.listRows(), len(r.visible), a.listRows())
	case "g", "home":
		r.selectionPinned = true
		r.win.home()
	case "G", "end":
		r.selectionPinned = true
		r.win.end(len(r.visible), a.listRows())
	case "enter":
		return a, a.selectResult()
	case "D":
		if r.win.cursor >= 0 && r.win.cursor < len(r.visible) {
			return a, a.downloadResultDirect(r.rows[r.visible[r.win.cursor]].res)
		}
	case "Y":
		if r.win.cursor >= 0 && r.win.cursor < len(r.visible) {
			return a, a.yankResult(r.rows[r.visible[r.win.cursor]].res)
		}
	}
	r.syncFlatSelection()
	return a, nil
}

func (a *App) updateResultsFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := &a.results
	switch msg.String() {
	case "esc":
		r.filtering = false
		r.filterCommitted = false
		r.filterIn.SetValue("")
		r.filterIn.Blur()
		r.refreshFilter()
		return a, nil
	case "enter":
		r.filterCommitted = true
		r.refreshFilter()
		if r.filterErr != "" {
			return a, nil
		}
		r.filtering = false
		r.filterIn.Blur()
		return a, nil
	}
	var cmd tea.Cmd
	r.filterIn, cmd = r.filterIn.Update(msg)
	r.filterCommitted = false
	r.refreshFilter()
	return a, cmd
}

// selectResult downloads the row under the flat-view cursor.
func (a *App) selectResult() tea.Cmd {
	r := &a.results
	if r.win.cursor < 0 || r.win.cursor >= len(r.visible) {
		return nil
	}
	return a.downloadResult(r.rows[r.visible[r.win.cursor]].res)
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
	if a.results.resolving {
		return nil
	}
	if res.Magnet != "" {
		return a.launchCmd(res.Magnet, res.Title, preview)
	}
	return a.startResolve(res, preview, false)
}

// yankResult copies the row's magnet, resolving it first when the provider's
// listing only carries a details-page link (the `Y` shortcut).
func (a *App) yankResult(res provider.Result) tea.Cmd {
	if res.Magnet != "" {
		return yankDownloadValue("magnet", res.Magnet)
	}
	if a.results.resolving {
		return nil
	}
	return a.startResolve(res, false, true)
}

// startResolve fetches the magnet behind a details-page row; the resolved
// magnet is routed by magnetResolvedMsg (yank to clipboard vs launch).
func (a *App) startResolve(res provider.Result, preview, yank bool) tea.Cmd {
	resolver := a.findResolver(res.Provider)
	if resolver == nil {
		return a.showError(res.Provider + ": cannot resolve magnet")
	}
	a.results.resolving = true
	a.results.resolveID++
	searchID := a.results.searchID
	resolveID := a.results.resolveID
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.SearchTimeout())
	a.results.resolveCancel = cancel
	return func() (msg tea.Msg) {
		defer cancel()
		defer guard(&msg, func(r any) tea.Msg {
			return magnetResolvedMsg{searchID: searchID, resolveID: resolveID, res: res, yank: yank, err: fmt.Errorf("resolve panicked: %v", r)}
		})
		magnet, err := resolver.ResolveMagnet(ctx, res)
		return magnetResolvedMsg{searchID: searchID, resolveID: resolveID, res: res, magnet: magnet, preview: preview, yank: yank, err: err}
	}
}

// cancelResolve invalidates the in-flight selected-row action without stopping
// the background search stream. Leaving Results should not later open a preview
// or copy a magnet the user no longer expects.
func (a *App) cancelResolve() {
	r := &a.results
	if r.resolveCancel != nil {
		r.resolveCancel()
		r.resolveCancel = nil
	}
	if r.resolving {
		r.resolveID++
	}
	r.resolving = false
}

// launchCmd either enters the preview screen or downloads immediately.
func (a *App) launchCmd(magnet, name string, preview bool) tea.Cmd {
	from := a.screen
	if preview {
		return func() (msg tea.Msg) {
			defer guard(&msg, func(r any) tea.Msg {
				return previewReadyMsg{magnet: magnet, name: name, from: from, err: fmt.Errorf("add panicked: %v", r)}
			})
			h, owned, err := a.eng.AddForPreview(magnet)
			return previewReadyMsg{hash: h, magnet: magnet, name: name, from: from, owned: owned, err: err}
		}
	}
	return a.addTorrentCmd(magnet, name)
}

func (a *App) launchTorrentURLPreviewCmd(rawURL, name string) tea.Cmd {
	from := a.screen
	return func() (msg tea.Msg) {
		defer guard(&msg, func(r any) tea.Msg {
			return previewReadyMsg{magnet: rawURL, name: name, from: from, err: fmt.Errorf("add panicked: %v", r)}
		})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		h, metaName, magnet, owned, err := a.eng.AddTorrentURLForPreview(ctx, rawURL)
		if metaName != "" {
			name = metaName
		}
		return previewReadyMsg{hash: h, magnet: magnet, name: name, from: from, owned: owned, err: err}
	}
}

func (a *App) launchTorrentFilePreviewCmd(path, name string) tea.Cmd {
	from := a.screen
	return func() (msg tea.Msg) {
		defer guard(&msg, func(r any) tea.Msg {
			return previewReadyMsg{magnet: path, name: name, from: from, err: fmt.Errorf("add panicked: %v", r)}
		})
		h, metaName, magnet, owned, err := a.eng.AddTorrentFileForPreview(path)
		if metaName != "" {
			name = metaName
		}
		return previewReadyMsg{hash: h, magnet: magnet, name: name, from: from, owned: owned, err: err}
	}
}

func (a *App) findResolver(name string) provider.MagnetResolver {
	for _, p := range a.agg.Providers() {
		if p.Name() == name || provider.DisplayName(p) == name {
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

// insertRow adds a result keeping rows sorted by the active mode. Two kinds of
// duplicate are dropped here: the same listing arriving twice (a provider
// retry), and the same torrent listed by a second index, which is folded into
// the row that already holds that infohash instead of taking a line of its own.
//
// It does NOT refresh the view - callers batch a refresh after draining a burst
// of streamed results, so the O(n) re-filter/re-group runs once per Update
// rather than once per result.
func (r *resultsModel) insertRow(res provider.Result) {
	if r.seen[res.Key()] {
		return
	}
	r.seen[res.Key()] = true
	row := r.newRow(res)
	if row.hash != "" && r.hashes[row.hash] && r.mergeRow(row) {
		return
	}
	if row.hash != "" {
		r.hashes[row.hash] = true
	}
	// first position where the existing row is not better than the new one
	pos := sort.Search(len(r.rows), func(i int) bool { return !r.betterThan(r.rows[i], row) })
	r.rows = append(r.rows, scoredRow{})
	copy(r.rows[pos+1:], r.rows[pos:])
	r.rows[pos] = row
}

// newRow caches everything derived from a result: its infohash identity, parsed
// tags, score, and noise verdict. Called again after a merge, because folding
// in another index changes the seeder count and trust flag the score reads.
func (r *resultsModel) newRow(res provider.Result) scoredRow {
	tags := rank.Parse(res.Title)
	return scoredRow{
		res:   res,
		hash:  res.InfoHash(),
		tags:  tags,
		score: rank.Score(res, tags, r.weights),
		noisy: rank.Noisy(r.query, res.Title, tags, res.Seeders),
	}
}

// mergeRow folds a second index's listing of a torrent already on the list into
// the existing row, so one torrent occupies one line however many indexes
// carry it - and, more usefully, so the magnet we hand the engine announces to
// every tracker any of those indexes knew about.
//
// The better-scoring of the two listings keeps the title, which is what the
// tag parser, the group label, and the ranker all read: whichever index
// answered first should not get to name the release. Rows are scanned rather
// than indexed by hash because a sorted insert shifts every position after it,
// and this only runs on an actual duplicate.
//
// Reports whether a row was found. A false means the hash index and the rows
// disagreed, which nothing can currently cause; the caller then adds the
// listing as its own row, because showing one torrent twice is a far better
// failure than dropping a search result on the floor.
func (r *resultsModel) mergeRow(incoming scoredRow) bool {
	for i := range r.rows {
		if r.rows[i].hash != incoming.hash {
			continue
		}
		keep, other := r.rows[i], incoming
		if other.score > keep.score {
			keep, other = other, keep
		}
		r.rows[i] = r.newRow(provider.Merge(keep.res, other.res))
		r.merged++
		r.resortPending = true // a higher seeder count can outrank the row above
		return true
	}
	return false
}

// resort re-orders all rows after a sort-mode change and rebuilds the view.
func (r *resultsModel) resort() {
	r.resortPending = true
	r.refreshFilter()
}

// refreshFilter recomputes visible rows and match highlights, first re-ordering
// rows when a merge (or a sort-mode change) invalidated their position.
func (r *resultsModel) refreshFilter() {
	if r.resortPending {
		r.resortPending = false
		sort.SliceStable(r.rows, func(i, j int) bool { return r.betterThan(r.rows[i], r.rows[j]) })
	}
	term := strings.TrimSpace(r.filterIn.Value())
	filter := parseResultFilter(term)
	if r.filtering && !r.filterCommitted {
		filter = parseLiveResultFilter(term)
	}
	r.filterErr = ""
	if filter.err != nil {
		r.filterErr = filter.err.Error()
		// A syntax correction belongs in the footer, not in the list. Keep all
		// valid text/facets applied and omit only invalid facets so a typo never
		// replaces useful results with a giant empty-state panel.
		filter = parseLiveResultFilter(term)
		filter.hint = ""
	}
	r.filterHint = filter.hint
	r.matched = make(map[int][]int)
	if term == "" {
		r.visible = r.visible[:0]
		for i := range r.rows {
			r.visible = append(r.visible, i)
		}
	} else {
		var eligible []int
		var titles []string
		for i, row := range r.rows {
			if filter.matches(row) {
				eligible = append(eligible, i)
				titles = append(titles, row.res.Title)
			}
		}
		r.visible = r.visible[:0]
		if filter.text == "" {
			r.visible = append(r.visible, eligible...)
		} else {
			for _, match := range fuzzy.Find(filter.text, titles) {
				idx := eligible[match.Index]
				r.visible = append(r.visible, idx)
				r.matched[idx] = match.MatchedIndexes
			}
		}
	}
	eligible := r.visible[:0]
	for _, idx := range r.visible {
		if r.pickPrefs.allows(r.rows[idx]) {
			eligible = append(eligible, idx)
		}
	}
	r.visible = eligible
	r.restoreFlatSelection()
	r.recomputeBest()
	if r.curated {
		r.rebuildPicks()
	}
	if r.grouped {
		r.rebuildGroups()
	}
}

// rowIdentity is stable across sorting, filtering and cross-provider merging.
// A known infohash is the strongest identity; detail-page-only rows fall back
// to the provider result key until a magnet is resolved.
func rowIdentity(row scoredRow) string {
	if row.hash != "" {
		return "hash:" + row.hash
	}
	return "row:" + row.res.Key()
}

func (r *resultsModel) syncFlatSelection() {
	if r.win.cursor < 0 || r.win.cursor >= len(r.visible) {
		r.selectedKey = ""
		return
	}
	idx := r.visible[r.win.cursor]
	if idx < 0 || idx >= len(r.rows) {
		r.selectedKey = ""
		return
	}
	r.selectedKey = rowIdentity(r.rows[idx])
}

func (r *resultsModel) restoreFlatSelection() {
	if r.selectionPinned && r.selectedKey != "" {
		for vi, idx := range r.visible {
			if rowIdentity(r.rows[idx]) == r.selectedKey {
				r.win.cursor = vi
				return
			}
		}
	}
	if !r.selectionPinned {
		r.win.home()
	}
	if r.win.cursor >= len(r.visible) {
		r.win.cursor = max(0, len(r.visible)-1)
	}
	r.syncFlatSelection()
}

// recomputeBest finds the single best pick among visible rows: the highest-
// ranked non-noisy result. Because r.rows is sorted best-first, the smallest
// visible non-noisy index wins. -1 when every visible row is noisy. This is the
// only row that earns the gold "best" badge, so it stays meaningful. It also
// refreshes meterMax, the shared scale every swarm meter is drawn against.
func (r *resultsModel) recomputeBest() {
	r.bestIdx = -1
	r.meterMax = 0
	for _, idx := range r.visible {
		r.meterMax = max(r.meterMax, r.rows[idx].res.Seeders)
		if r.rows[idx].noisy {
			continue
		}
		if r.bestIdx == -1 || idx < r.bestIdx {
			r.bestIdx = idx
		}
	}
}

func (a *App) resultsDetailVisible() bool { return a.bodyHeight() >= 14 }

// listRows is the number of visible flat-result rows. On a tall terminal the
// selected result gets a five-line decision panel, so the list pays that space
// up front and paging stays aligned with what is actually visible.
func (a *App) listRows() int {
	rows := max(1, a.bodyHeight()-2)
	if a.resultsDetailVisible() {
		rows = max(1, rows-6)
	}
	return rows
}

// graphRows is the window height of the grouped list: listRows minus the
// detail panel (5 lines) when the terminal is tall enough to show one. Key
// handling and rendering must agree on this so paging moves by a real page.
func (a *App) graphRows() int {
	rows := max(1, a.bodyHeight()-2)
	if a.resultsDetailVisible() {
		return max(1, rows-6)
	}
	return rows
}

func (a *App) viewResults() string {
	r := &a.results
	if r.pickPanel {
		return a.viewPickFilters()
	}
	if r.curated && !r.filtering {
		return a.viewPicks()
	}
	width := a.contentWidth()
	listH := a.listRows()

	var b strings.Builder
	if r.query != "" || r.searching || len(r.rows) > 0 {
		b.WriteString(r.statusLine(a.agg) + "\n")
	}

	if len(r.visible) == 0 {
		title, detail := "No search yet", "esc back · type a title or paste a torrent link to begin"
		switch {
		case r.searching && len(r.rows) == 0:
			title, detail = "Searching your sources…", "Results appear as each source responds. You can switch screens while waiting."
		case len(r.rows) > 0:
			title, detail = "No results match these filters", "/ edit filters · clear the filter to show all results"
		case r.query != "":
			title, detail = "No results for this search", "Check the source status above, or press esc to edit your search."
		}
		if !r.filtering {
			b.WriteString("\n" + styleFg.Bold(true).Render(title) + "\n" + styleDim.Render(detail))
			return a.chrome("results", b.String(), hints(hint("esc", "search"), hint("/", "filters"), hint("?", "keys")))
		}
	}

	if r.grouped {
		b.WriteString(a.graphColumns(width) + "\n")
		graphH := a.graphRows()
		detail := a.bodyHeight() >= 14
		// Shrink the window to the rows that exist so the detail panel sits
		// right under the list instead of drifting to the bottom of a tall
		// terminal with a void in between.
		if n := len(r.navItems()); n < graphH {
			graphH = max(1, n)
		}
		b.WriteString(a.viewGraph(width, graphH))
		if detail {
			b.WriteString("\n")
			b.WriteString(a.graphDetail(width))
		}
		b.WriteString("\n")
	} else {
		lay := newResultsLayout(width)
		b.WriteString(styleFaint.Render(fmt.Sprintf("   %-*s %*s %*s %*s %*s  %s",
			lay.titleW, "title", lay.sizeW, "size", lay.seedW, "S", lay.leechW, "L", lay.resW, "res", "prov")) + "\n")

		b.WriteString(renderWindow(&r.win, len(r.visible), listH, width, func(vi int, selected bool) string {
			idx := r.visible[vi]
			row := r.rows[idx]
			line := fmt.Sprintf("%s %s %*s %s %s %s  %s",
				healthDot(row.res.Seeders),
				r.renderTitle(idx, lay.titleW),
				lay.sizeW, truncate(row.res.Size, lay.sizeW),
				styleSeeders.Render(fmt.Sprintf("%*d", lay.seedW, row.res.Seeders)),
				styleLeechers.Render(fmt.Sprintf("%*d", lay.leechW, row.res.Leechers)),
				styleFaint.Render(fmt.Sprintf("%*s", lay.resW, row.tags.Resolution.String())),
				sourceTag(row.res, false),
			)
			if !selected && row.noisy {
				line = styleFaint.Render(line)
			}
			return line
		}))
		b.WriteString("\n")
		if a.resultsDetailVisible() {
			b.WriteString(a.resultDetail(width))
			b.WriteString("\n")
		}
	}

	var help string
	switch {
	case r.filtering:
		help = r.filterIn.View()
		if r.filterErr != "" {
			help += "  " + styleErr.Render(r.filterErr)
		} else if r.filterHint != "" {
			help += "  " + styleFaint.Render(r.filterHint)
		} else {
			help += "  " + styleFaint.Render("try res:1080p  seeders:>20  size:<8gb  is:trusted")
		}
	case r.resolving:
		help = styleDim.Render("resolving magnet…")
	default:
		// Both hand-written hint rows are gone: the strip is generated from the
		// one keymap table now, so the grouped/flat split lives there. The
		// "smart filter" rename that landed with the filter syntax moved into
		// that table with them.
		help = a.keyStrip(a.helpBudget(width))
	}

	ctx := "results"
	if r.query != "" {
		ctx = "results · " + r.query
	}
	return a.chrome(ctx, b.String(), help)
}

// resultDetail turns the flat live list into a decision view: the highlighted
// row remains compact above, while the facts needed before Enter stay readable
// as providers continue to stream results around it.
func (a *App) resultDetail(width int) string {
	r := &a.results
	lines := []string{rule(width)}
	if r.win.cursor < 0 || r.win.cursor >= len(r.visible) {
		return strings.Join(append(lines, styleDim.Render("no result selected"), "", "", ""), "\n")
	}
	idx := r.visible[r.win.cursor]
	if idx < 0 || idx >= len(r.rows) {
		return strings.Join(append(lines, styleDim.Render("no result selected"), "", "", ""), "\n")
	}
	row := r.rows[idx]
	trust := styleFaint.Render("untrusted")
	if row.res.Trusted {
		trust = styleOK.Render("trusted")
	}
	quality := []string{}
	if resolution := row.tags.Resolution.String(); resolution != "" {
		quality = append(quality, resolution)
	}
	if source := row.tags.Source.String(); source != "" {
		quality = append(quality, source)
	}
	if row.tags.Codec != "" {
		quality = append(quality, row.tags.Codec)
	}
	noise := styleOK.Render("looks clean")
	if reasons := rank.NoiseReasons(r.query, row.res.Title, row.tags, row.res.Seeders); len(reasons) > 0 {
		noise = styleHealthMid.Render("check: " + strings.Join(reasons, ", "))
	}
	size := strings.TrimSpace(row.res.Size)
	if row.res.SizeBytes > 0 {
		size = humanBytes(row.res.SizeBytes)
	} else if size == "" {
		size = "size unknown"
	}
	lines = append(lines,
		styleFg.Render(truncate(row.res.Title, width)),
		truncate(fmt.Sprintf("%s  %s  %s  score %.1f", sourceCol(row.res, false), trust, strings.Join(quality, " · "), row.score), width),
		truncate(fmt.Sprintf("%s %d   %s %d   %s   %s", styleSeeders.Render("S"), row.res.Seeders, styleLeechers.Render("L"), row.res.Leechers, size, magnetCell(row.res)), width),
		truncate(noise+styleFaint.Render(" · "+plural(len(row.res.Trackers()), "tracker")), width),
	)
	return strings.Join(lines, "\n")
}

// renderTitle pads/truncates and highlights fuzzy-matched runes.
func (r *resultsModel) renderTitle(idx, width int) string {
	title := truncate(r.rows[idx].res.Title, width)
	pad := max(0, width-lipgloss.Width(title))
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
	providers := agg.Providers()
	chips := make([]string, 0, len(providers))
	failed := 0
	hidden := 0
	for _, p := range providers {
		name := provider.DisplayName(p)
		ev, ok := r.status[name]
		hidden += ev.Hidden
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
		if strings.TrimSpace(r.filterIn.Value()) != "" || r.pickPrefs.resolutions != 0 || r.pickPrefs.size != 0 {
			head = styleOK.Render(fmt.Sprintf("%d/%d results", len(r.visible), n))
		} else {
			head = styleOK.Render(fmt.Sprintf("%d results", n))
		}
	case r.searching:
		head = styleDim.Render("searching live…")
	case hidden > 0:
		head = styleDim.Render("no visible results")
	case failed > 0:
		head = styleErr.Render("no results - sources unavailable, try again")
	default:
		head = styleDim.Render("no results")
	}

	line := head
	if r.grouped && len(r.groups) > 0 {
		line += styleFaint.Render(fmt.Sprintf("  · %d groups", len(r.groups)))
	}
	if r.merged > 0 {
		line += styleFaint.Render(fmt.Sprintf("  · %d merged", r.merged))
	}
	if hidden > 0 {
		line += styleFaint.Render(fmt.Sprintf("  · %d hidden", hidden))
	}
	line += styleDim.Render("   ") + strings.Join(chips, styleDim.Render(" · "))
	if r.searching && len(r.rows) > 0 {
		line += styleBrand.Render("  · live")
	}
	return line
}
