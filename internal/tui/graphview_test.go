package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

// TestGraphLeafAligns locks in the ANSI-aware padding: providers with very
// different name lengths must still line their columns up. Two middle leaves
// (not best, not last, magnet-ready) carry no trailing markers, so equal total
// width means the provider/seeders/size columns are aligned.
func TestGraphLeafAligns(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Provider: "knaben", Size: "1 GiB", Seeders: 1, Magnet: "magnet:?x"}},
		{res: provider.Result{Provider: "yts", Size: "1.0 GiB", Seeders: 5, Magnet: "magnet:?x"}},
		{res: provider.Result{Provider: "tpb-movies", Size: "12.3 GiB", Seeders: 1200, Magnet: "magnet:?x"}},
		{res: provider.Result{Provider: "eztv", Size: "700 MiB", Seeders: 9, Magnet: "magnet:?x"}},
	}}
	g := &group{rowIdx: []int{0, 1, 2, 3}}
	a := lipgloss.Width(r.graphLeaf(g, 1, 80, false))
	b := lipgloss.Width(r.graphLeaf(g, 2, 80, false))
	if a != b {
		t.Fatalf("leaf widths differ: %d vs %d (columns not aligned)", a, b)
	}
}

// TestGroupsDefaultCollapsed locks in the "collapse harder" behavior: a
// multi-source group arrives collapsed (one nav line), and a user's expand is
// preserved across the streaming rebuild.
func TestGroupsDefaultCollapsed(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Example 1080p", Provider: "knaben", Seeders: 100}, tags: rank.Parse("Example 1080p")},
		{res: provider.Result{Title: "Example 1080p", Provider: "1337x", Seeders: 20}, tags: rank.Parse("Example 1080p")},
	}, visible: []int{0, 1}}

	r.rebuildGroups()
	if n := len(r.navItems()); n != 1 {
		t.Fatalf("collapsed group: %d nav items, want 1 (header only)", n)
	}

	r.groups[0].collapsed = false
	if n := len(r.navItems()); n != 3 {
		t.Fatalf("expanded group: %d nav items, want 3 (header + 2 leaves)", n)
	}

	r.rebuildGroups() // expand must survive a rebuild
	if n := len(r.navItems()); n != 3 {
		t.Fatalf("after rebuild: %d nav items, want expand preserved (3)", n)
	}
}

// TestNoisyDoesNotFrontGroup guards against the overall-best pick vanishing:
// when a noisy source outranks the clean champion in the same (collapsed) group,
// the clean row must float to rowIdx[0] so the header summarizes it, wears the
// "best" badge, and enter downloads it - not the noisy source.
func TestNoisyDoesNotFrontGroup(t *testing.T) {
	r := &resultsModel{grouped: true, bestIdx: -1, rows: []scoredRow{
		{res: provider.Result{Title: "Movie 1080p", Provider: "knaben", Size: "2 GiB", Seeders: 900, Magnet: "magnet:?x"}, tags: rank.Parse("Movie 1080p"), noisy: true},
		{res: provider.Result{Title: "Movie 1080p", Provider: "yts", Size: "1 GiB", Seeders: 300, Magnet: "magnet:?x"}, tags: rank.Parse("Movie 1080p"), noisy: false},
	}, visible: []int{0, 1}}
	r.recomputeBest()
	r.rebuildGroups()

	if got := r.groups[0].rowIdx[0]; got != 1 {
		t.Fatalf("rowIdx[0] = %d, want the clean row (1)", got)
	}
	if h := r.graphHeader(&r.groups[0], 90, false); !strings.Contains(h, "best") || !strings.Contains(h, "S300") {
		t.Fatalf("collapsed header should show clean champion with best: %q", h)
	}
	res, ok := r.graphResultForItem(navItem{group: 0, leaf: -1})
	if !ok || res.Seeders != 300 {
		t.Fatalf("enter-on-header downloads S%d, want clean S300", res.Seeders)
	}
}

// TestGraphFlatLeafHasNoBar asserts single-source rows carry no meter.
func TestGraphFlatLeafHasNoBar(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Solo 1080p", Provider: "yts", Size: "1 GiB", Seeders: 42, Magnet: "magnet:?x"}},
	}}
	got := r.graphFlatLeaf(0, 100, false)
	if strings.ContainsAny(got, "▮▯") {
		t.Fatalf("flat leaf has a meter: %q", got)
	}
}

// TestSelectedRowsRenderPlain proves selected rows carry no inline ANSI, so the
// single selection background/foreground isn't broken mid-line.
func TestSelectedRowsRenderPlain(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Example 1080p", Provider: "knaben", Size: "1 GiB", Seeders: 100, Magnet: "magnet:?x"}},
		{res: provider.Result{Title: "Example 1080p", Provider: "1337x", Size: "1.1 GiB", Seeders: 20}},
	}}
	g := &group{label: "example 1080p", rowIdx: []int{0, 1}}
	checks := map[string]string{
		"header": r.graphHeader(g, 100, true),
		"leaf":   r.graphLeaf(g, 0, 100, true),
		"flat":   r.graphFlatLeaf(0, 100, true),
	}
	for name, got := range checks {
		if strings.Contains(got, "\x1b") {
			t.Fatalf("selected %s row has inline ANSI: %q", name, got)
		}
	}
}

// TestNoisyLeafDimmed asserts a noisy row is wrapped in the faint color. It
// forces a color profile since the test process has no TTY (colors stripped).
func TestNoisyLeafDimmed(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)

	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Dead 1080p", Provider: "knaben", Size: "1 GiB", Seeders: 0}, noisy: true},
	}}
	g := &group{rowIdx: []int{0}}
	got := r.graphLeaf(g, 0, 100, false)
	if !strings.Contains(got, "38;5;240") {
		t.Fatalf("noisy leaf not dimmed with faint color: %q", got)
	}
}

// TestGraphHeaderFormat checks the collapsed heading carries the source count,
// top-source summary, provider tag, seeders, and size - and the "best" badge
// when its top source is the overall pick.
func TestGraphHeaderFormat(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Example 1080p", Provider: "knaben", Size: "2 GiB", Seeders: 100}},
		{res: provider.Result{Title: "Example 1080p", Provider: "yts", Size: "2.1 GiB", Seeders: 20}},
	}, bestIdx: 0}
	g := &group{label: "example 1080p", rowIdx: []int{0, 1}}
	got := r.graphHeader(g, 100, false)
	for _, want := range []string{"best", "[knaben]", "S100", "2 GiB", "×2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("header %q missing %q", got, want)
		}
	}
}

// TestBestBadgeOnlyOnChampion locks in the single-overall-best rule: only the
// group/row holding r.bestIdx shows "best"; everything else omits it.
func TestBestBadgeOnlyOnChampion(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Alpha 1080p", Provider: "knaben", Size: "2 GiB", Seeders: 900, Magnet: "magnet:?x"}},
		{res: provider.Result{Title: "Beta 1080p", Provider: "yts", Size: "1 GiB", Seeders: 300, Magnet: "magnet:?x"}},
	}, visible: []int{0, 1}}
	r.recomputeBest() // best = row 0 (highest rank, not noisy)
	if r.bestIdx != 0 {
		t.Fatalf("bestIdx = %d, want 0", r.bestIdx)
	}
	if got := r.graphFlatLeaf(0, 100, false); !strings.Contains(got, "best") {
		t.Fatalf("champion flat row missing best: %q", got)
	}
	if got := r.graphFlatLeaf(1, 100, false); strings.Contains(got, "best") {
		t.Fatalf("non-champion flat row shows best: %q", got)
	}
}

func TestGraphChildFormat(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Example 1080p", Provider: "knaben", Size: "2 GiB", Seeders: 100, Magnet: "magnet:?x", Trusted: true}},
		{res: provider.Result{Title: "Example 1080p", Provider: "yts", Size: "2.1 GiB", Seeders: 20}},
	}}
	g := &group{label: "example 1080p", rowIdx: []int{0, 1}}
	got := r.graphLeaf(g, 0, 100, false)
	for _, want := range []string{"#1", "[knaben]", "S100", "2 GiB", "trusted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("child %q missing %q", got, want)
		}
	}
	// Expanded leaves never carry the "best" badge - #1 already marks in-group best.
	if strings.Contains(got, "best") {
		t.Fatalf("child row should not show best badge: %q", got)
	}
	if strings.ContainsAny(got, "★✓") {
		t.Fatalf("child contains legacy glyph badge: %q", got)
	}
	// Expanded leaves DO carry the swarm-health meter (that is the point of expanding).
	if !strings.ContainsAny(got, "▮▯") {
		t.Fatalf("child row missing health meter: %q", got)
	}
}

// TestGraphRowsAlign proves the shared grid works: a header, an expanded leaf,
// and a flat single-source row all render to the same total width, so their
// fixed columns line up vertically.
func TestGraphRowsAlign(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Title: "Example 1080p", Provider: "knaben", Size: "2 GiB", Seeders: 100, Magnet: "magnet:?x"}},
		{res: provider.Result{Title: "Example 1080p", Provider: "1337x", Size: "2.1 GiB", Seeders: 20, Magnet: "magnet:?x"}},
	}}
	g := &group{label: "example 1080p", rowIdx: []int{0, 1}}
	widths := []int{
		lipgloss.Width(r.graphHeader(g, 100, false)),
		lipgloss.Width(r.graphLeaf(g, 0, 100, false)),
		lipgloss.Width(r.graphFlatLeaf(0, 100, false)),
	}
	for i := 1; i < len(widths); i++ {
		if widths[i] != widths[0] {
			t.Fatalf("row widths differ: %v (columns not aligned)", widths)
		}
	}
}

// TestGraphLeafGuides checks the tree connectors: inner leaves get ├, the last
// leaf gets └.
func TestGraphLeafGuides(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Provider: "knaben", Size: "1 GiB", Seeders: 5, Magnet: "magnet:?x"}},
		{res: provider.Result{Provider: "yts", Size: "1 GiB", Seeders: 5, Magnet: "magnet:?x"}},
		{res: provider.Result{Provider: "1337x", Size: "1 GiB", Seeders: 5, Magnet: "magnet:?x"}},
	}}
	g := &group{rowIdx: []int{0, 1, 2}}
	if got := r.graphLeaf(g, 0, 100, false); !strings.Contains(got, "├") {
		t.Fatalf("inner leaf missing ├ guide: %q", got)
	}
	if got := r.graphLeaf(g, 2, 100, false); !strings.Contains(got, "└") {
		t.Fatalf("last leaf missing └ guide: %q", got)
	}
}

// TestGraphMeterDropsOnNarrow asserts the health meter disappears on narrow
// terminals so the decision columns still fit.
func TestGraphMeterDropsOnNarrow(t *testing.T) {
	r := &resultsModel{rows: []scoredRow{
		{res: provider.Result{Provider: "knaben", Size: "1 GiB", Seeders: 5, Magnet: "magnet:?x"}},
		{res: provider.Result{Provider: "yts", Size: "1 GiB", Seeders: 5, Magnet: "magnet:?x"}},
	}}
	g := &group{rowIdx: []int{0, 1}}
	if got := r.graphLeaf(g, 0, 50, false); strings.ContainsAny(got, "▮▯") {
		t.Fatalf("narrow leaf still shows a meter: %q", got)
	}
}

// TestGraphDirectKey wires the D shortcut to a direct (no-preview) download of
// the row under the cursor, matching the flat view.
func TestGraphDirectKey(t *testing.T) {
	a := &App{}
	a.results.rows = []scoredRow{
		{res: provider.Result{Title: "Solo 1080p", Provider: "yts", Magnet: "magnet:?x"}},
	}
	a.results.groups = []group{{rowIdx: []int{0}}}
	_, cmd := a.updateGraphKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")})
	if cmd == nil {
		t.Fatalf("D did not trigger a download command")
	}
}

// TestGraphCollapseSnapsToHeader locks in the cursor following a fold: after
// collapsing from a leaf, the cursor lands on that group's header, not a
// neighbor that shifted underneath it.
func TestGraphCollapseSnapsToHeader(t *testing.T) {
	a := &App{}
	a.results.groups = []group{
		{rowIdx: []int{0, 1}, collapsed: false},
		{rowIdx: []int{2, 3}, collapsed: true},
	}
	a.results.rows = make([]scoredRow, 4)
	// nav items expanded: [g0 header, g0 leaf0, g0 leaf1, g1 header]; cursor on g0 leaf1.
	a.results.gwin.cursor = 2
	a.updateGraphKeys(tea.KeyMsg{Type: tea.KeyLeft})
	if !a.results.groups[0].collapsed {
		t.Fatalf("left did not collapse group 0")
	}
	if a.results.gwin.cursor != 0 {
		t.Fatalf("cursor = %d after collapse, want group-0 header (0)", a.results.gwin.cursor)
	}
}

// TestStatusLineShowsGroups asserts the grouped view surfaces the group count.
func TestStatusLineShowsGroups(t *testing.T) {
	r := &resultsModel{
		grouped: true,
		status:  map[string]aggregator.StatusEvent{},
		rows: []scoredRow{
			{res: provider.Result{Title: "A"}},
			{res: provider.Result{Title: "B"}},
			{res: provider.Result{Title: "C"}},
		},
		groups: []group{{}, {}, {}},
	}
	got := r.statusLine(&aggregator.Aggregator{})
	if !strings.Contains(got, "3 groups") {
		t.Fatalf("grouped status line missing group count: %q", got)
	}
}

func TestGraphResultForItem(t *testing.T) {
	r := &resultsModel{
		rows: []scoredRow{
			{res: provider.Result{Title: "best", Provider: "knaben"}},
			{res: provider.Result{Title: "child", Provider: "yts"}},
		},
		groups: []group{{rowIdx: []int{0, 1}}},
	}
	res, ok := r.graphResultForItem(navItem{group: 0, leaf: -1})
	if !ok || res.Title != "best" {
		t.Fatalf("header selected %q, ok %v; want best", res.Title, ok)
	}
	res, ok = r.graphResultForItem(navItem{group: 0, leaf: 1})
	if !ok || res.Title != "child" {
		t.Fatalf("child selected %q, ok %v; want child", res.Title, ok)
	}
}

func TestGraphExpandCollapseKeys(t *testing.T) {
	a := &App{}
	a.results.groups = []group{{rowIdx: []int{0, 1}, collapsed: true}}

	a.updateGraphKeys(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	if a.results.groups[0].collapsed {
		t.Fatalf("space did not expand group")
	}
	a.updateGraphKeys(tea.KeyMsg{Type: tea.KeyLeft})
	if !a.results.groups[0].collapsed {
		t.Fatalf("left did not collapse group")
	}
	a.updateGraphKeys(tea.KeyMsg{Type: tea.KeyRight})
	if a.results.groups[0].collapsed {
		t.Fatalf("right did not expand group")
	}
}

func TestSeederBar(t *testing.T) {
	cases := []struct {
		name        string
		seeders     int
		maxSeeders  int
		cells       int
		wantWidth   int
		wantFilled  bool
		wantEmpty   bool
		wantNoValue bool
	}{
		{"zero", 0, 10, 8, 8, false, false, true},
		{"max", 10, 10, 8, 8, true, false, false},
		{"mid", 3, 10, 8, 8, true, false, false},
		{"tiny", 10, 10, 2, 0, false, true, false},
		{"no max", 10, 0, 8, 8, false, false, true},
	}
	for _, tc := range cases {
		got := seederBar(tc.seeders, tc.maxSeeders, tc.cells)
		if tc.wantEmpty {
			if got != "" {
				t.Fatalf("%s: got %q, want empty", tc.name, got)
			}
			continue
		}
		if w := lipgloss.Width(got); w != tc.wantWidth {
			t.Fatalf("%s: width = %d, want %d", tc.name, w, tc.wantWidth)
		}
		if tc.wantFilled && !strings.Contains(got, "▮") {
			t.Fatalf("%s: bar has no filled cells: %q", tc.name, got)
		}
		if tc.wantNoValue && strings.Contains(got, "▮") {
			t.Fatalf("%s: bar should be empty-value only: %q", tc.name, got)
		}
	}
}

func TestGroupSizeSpread(t *testing.T) {
	rows := []scoredRow{
		{res: provider.Result{SizeBytes: 100}},
		{res: provider.Result{SizeBytes: 102}},
		{res: provider.Result{SizeBytes: 105}},
	}
	median, spread := groupSizeSpread(rows, []int{0, 1, 2})
	if median != 102 {
		t.Fatalf("median = %d, want 102", median)
	}
	if spread > 5 {
		t.Fatalf("spread = %.2f, want <= 5", spread)
	}

	rows = []scoredRow{
		{res: provider.Result{SizeBytes: 100}},
		{res: provider.Result{SizeBytes: 200}},
		{res: provider.Result{SizeBytes: 300}},
	}
	median, spread = groupSizeSpread(rows, []int{0, 1, 2})
	if median != 200 {
		t.Fatalf("median = %d, want 200", median)
	}
	if spread < 49 || spread > 51 {
		t.Fatalf("spread = %.2f, want about 50", spread)
	}

	median, spread = groupSizeSpread([]scoredRow{{res: provider.Result{}}}, []int{0})
	if median != 0 || spread != 0 {
		t.Fatalf("unknown sizes = (%d, %.2f), want zeroes", median, spread)
	}
}

func TestGraphDetailDoesNotPanic(t *testing.T) {
	a := &App{}
	if got := a.graphDetail(80); !strings.Contains(got, "no source selected") {
		t.Fatalf("empty detail = %q", got)
	}

	a.results.rows = []scoredRow{
		{res: provider.Result{Title: "Example 1080p", Provider: "knaben", Size: "1 GiB", SizeBytes: 1 << 30, Seeders: 100, Leechers: 10, Magnet: "magnet:?x", Trusted: true}},
		{res: provider.Result{Title: "Example 1080p", Provider: "1337x", Size: "1.1 GiB", SizeBytes: 1100 << 20, Seeders: 20, Leechers: 5}},
	}
	a.results.groups = []group{{label: "Example 1080p", rowIdx: []int{0, 1}}}
	a.results.gwin.cursor = 1 // first leaf
	got := a.graphDetail(80)
	if !strings.Contains(got, "magnet ready") || !strings.Contains(got, "trusted") {
		t.Fatalf("leaf detail missing markers: %q", got)
	}
	if !strings.Contains(got, "#1 of 2") || !strings.Contains(got, "1 GiB") {
		t.Fatalf("leaf detail missing source count or size: %q", got)
	}
	if strings.ContainsAny(got, "★✓") {
		t.Fatalf("detail contains glyph badge: %q", got)
	}
}

func TestGraphHeaderDetail(t *testing.T) {
	a := &App{}
	a.results.rows = []scoredRow{
		{res: provider.Result{Title: "Example 1080p", Provider: "knaben", Size: "1 GiB", SizeBytes: 1 << 30, Seeders: 100, Magnet: "magnet:?x"}},
		{res: provider.Result{Title: "Example 1080p", Provider: "1337x", Size: "2 GiB", SizeBytes: 2 << 30, Seeders: 20}, noisy: true},
	}
	a.results.groups = []group{{label: "Example 1080p", rowIdx: []int{0, 1}}}
	got := a.graphDetail(80)
	for _, want := range []string{"best", "2 providers", "total S120", "size varies", "warnings"} {
		if !strings.Contains(got, want) {
			t.Fatalf("header detail %q missing %q", got, want)
		}
	}
	if strings.ContainsAny(got, "★✓") {
		t.Fatalf("header detail contains glyph badge: %q", got)
	}
}
