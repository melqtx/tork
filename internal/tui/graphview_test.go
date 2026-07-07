package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/melqtx/tork/internal/provider"
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
	a := lipgloss.Width(r.graphLeaf(g, 1, 80))
	b := lipgloss.Width(r.graphLeaf(g, 2, 80))
	if a != b {
		t.Fatalf("leaf widths differ: %d vs %d (columns not aligned)", a, b)
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
		if tc.wantFilled && !strings.Contains(got, "▓") {
			t.Fatalf("%s: bar has no filled cells: %q", tc.name, got)
		}
		if tc.wantNoValue && strings.Contains(got, "▓") {
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
	if !strings.Contains(got, "magnet ready") || !strings.Contains(got, "✓trusted") {
		t.Fatalf("leaf detail missing markers: %q", got)
	}
}
