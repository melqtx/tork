package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

func filterTestRow(title, providerName, category string, size int64, seeders int, trusted bool) scoredRow {
	result := provider.Result{
		Title: title, Provider: providerName, Category: category,
		SizeBytes: size, Seeders: seeders, Trusted: trusted,
	}
	return scoredRow{res: result, tags: rank.Parse(title)}
}

func TestResultFilterComposesStructuredPredicates(t *testing.T) {
	row := filterTestRow(
		"Example S02 Complete 1080p WEB-DL x265 HDR",
		"nyaa", "Anime", 7<<30, 42, true,
	)
	for _, input := range []string{
		"res:1080p",
		"seeders:>20 size:<8gb",
		"source:web-dl codec:hevc",
		"provider:nyaa category:anime",
		"is:trusted is:hdr is:pack",
		"-source:cam -is:dv",
		"seed:42 size:7gb",
	} {
		filter := parseResultFilter(input)
		if filter.err != nil {
			t.Fatalf("%q: %v", input, filter.err)
		}
		if !filter.matches(row) {
			t.Errorf("%q did not match row", input)
		}
	}
}

func TestResultFilterRejectsNonMatches(t *testing.T) {
	row := filterTestRow("Example 720p BluRay x264", "yts", "Movies", 12<<30, 3, false)
	for _, input := range []string{
		"res:1080p",
		"seeders:>20",
		"size:<8gb",
		"source:web",
		"codec:av1",
		"provider:nyaa",
		"category:anime",
		"is:trusted",
		"-source:bluray",
	} {
		filter := parseResultFilter(input)
		if filter.err != nil {
			t.Fatalf("%q: %v", input, filter.err)
		}
		if filter.matches(row) {
			t.Errorf("%q unexpectedly matched row", input)
		}
	}
}

func TestResultFilterKeepsPlainTextForFuzzyMatch(t *testing.T) {
	filter := parseResultFilter("big bunny res:1080p unknown:value")
	if filter.err != nil {
		t.Fatal(filter.err)
	}
	if filter.text != "big bunny unknown:value" {
		t.Fatalf("text = %q", filter.text)
	}
	if len(filter.predicates) != 1 {
		t.Fatalf("predicates = %d, want 1", len(filter.predicates))
	}
}

func TestResultFilterReportsHelpfulSyntaxErrors(t *testing.T) {
	for _, input := range []string{
		"seeders:many",
		"size:<8",
		"size:<8parsecs",
		"res:12k",
		"source:vhs",
		"codec:mpeg2",
		"is:fancy",
	} {
		filter := parseResultFilter(input)
		if filter.err == nil {
			t.Errorf("%q: expected error", input)
		}
	}
}

func TestLiveResultFilterKeepsRowsWhileFacetIsIncomplete(t *testing.T) {
	row := filterTestRow("Example 1080p WEB-DL", "alpha", "movies", 4<<30, 42, false)
	for _, input := range []string{"res:", "res:1", "res:108", "size:<8", "seeders:>"} {
		filter := parseLiveResultFilter(input)
		if filter.err != nil {
			t.Fatalf("%q raised a live error: %v", input, filter.err)
		}
		if filter.hint == "" {
			t.Fatalf("%q has no completion hint", input)
		}
		if !filter.matches(row) {
			t.Fatalf("%q hid the row while its last token was incomplete", input)
		}
	}
}

func TestLiveResultFilterDefersErrorsUntilSubmit(t *testing.T) {
	filter := parseLiveResultFilter("res:12k ")
	if filter.err != nil || filter.hint == "" {
		t.Fatalf("live filter error=%v hint=%q; validation should wait for Enter", filter.err, filter.hint)
	}
	if strict := parseResultFilter("res:12k"); strict.err == nil {
		t.Fatal("strict submit parser accepted invalid resolution")
	}
}

func TestSubmittingInvalidFilterKeepsEditorOpen(t *testing.T) {
	r := newResultsModel(rank.DefaultWeights())
	r.filtering = true
	r.filterIn.SetValue("res:12k")
	a := &App{results: r}

	a.updateResultsFilter(tea.KeyMsg{Type: tea.KeyEnter})
	if !a.results.filtering || a.results.filterErr == "" {
		t.Fatalf("filtering=%v error=%q; invalid submit should stay editable", a.results.filtering, a.results.filterErr)
	}
}

func TestIncompleteResolutionFilterRendersAsGuidanceNotError(t *testing.T) {
	r := newResultsModel(rank.DefaultWeights())
	r.insertRow(provider.Result{Title: "Example 1080p WEB-DL", Provider: "alpha", Seeders: 10})
	r.filtering = true
	r.filterIn.SetValue("res:108")
	r.refreshFilter()
	a := &App{
		width: 100, height: 30, screen: screenResults, results: r,
		agg: aggregator.New(nil, 0, 0), downloads: newDownloadsModel(),
	}

	view := a.viewResults()
	if !strings.Contains(view, "resolution  480p") {
		t.Fatalf("live filter view has no completion guidance: %q", view)
	}
	for _, unwanted := range []string{"unknown resolution", "no result selected"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("live filter view still contains %q", unwanted)
		}
	}
}

func TestRefreshFilterCombinesFuzzyTextAndFacets(t *testing.T) {
	model := newResultsModel(rank.DefaultWeights())
	for _, row := range []provider.Result{
		{Title: "Big Buck Bunny 1080p WEB-DL x265", Provider: "alpha", SizeBytes: 4 << 30, Seeders: 30},
		{Title: "Big Buck Bunny 720p BluRay x264", Provider: "beta", SizeBytes: 2 << 30, Seeders: 80},
		{Title: "Other Movie 1080p WEB-DL x265", Provider: "alpha", SizeBytes: 4 << 30, Seeders: 30},
	} {
		model.insertRow(row)
	}

	model.filterIn.SetValue("big bunny res:1080p seeders:>20 codec:x265")
	model.refreshFilter()
	if model.filterErr != "" {
		t.Fatal(model.filterErr)
	}
	if len(model.visible) != 1 {
		t.Fatalf("visible = %v, want one result", model.visible)
	}
	got := model.rows[model.visible[0]].res.Title
	if !strings.Contains(got, "1080p") {
		t.Fatalf("visible title = %q", got)
	}
	if len(model.matched[model.visible[0]]) == 0 {
		t.Fatal("fuzzy title positions were not preserved")
	}
}

func TestRefreshFilterSurfacesErrorWithoutBlankingRows(t *testing.T) {
	model := newResultsModel(rank.DefaultWeights())
	model.insertRow(provider.Result{Title: "Example 1080p", Provider: "alpha", Seeders: 10})
	model.filterIn.SetValue("size:huge")
	model.refreshFilter()
	if model.filterErr == "" {
		t.Fatal("expected filter error")
	}
	if len(model.visible) != 1 {
		t.Fatalf("visible = %v, want the usable result set retained", model.visible)
	}
}

// A merged row fronts one index but represents several. Filtering by any of
// them must find it, or cross-index merging would quietly make provider:
// filters lie about which indexes carry a torrent.
func TestProviderFilterMatchesMergedSources(t *testing.T) {
	row := scoredRow{res: provider.Result{
		Title:    "Some Release 1080p",
		Provider: "yts",
		AlsoOn:   []string{"1337x", "knaben"},
	}}

	for _, name := range []string{"yts", "knaben", "1337x", "KNABEN"} {
		if !parseResultFilter("provider:" + name).matches(row) {
			t.Errorf("provider:%s did not match a row merged from %v + %s",
				name, row.res.AlsoOn, row.res.Provider)
		}
	}
	if parseResultFilter("provider:nyaa").matches(row) {
		t.Error("provider:nyaa matched a row no nyaa listing contributed to")
	}
}
