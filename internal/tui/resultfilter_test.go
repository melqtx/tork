package tui

import (
	"strings"
	"testing"

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

func TestRefreshFilterSurfacesErrorAndShowsNoRows(t *testing.T) {
	model := newResultsModel(rank.DefaultWeights())
	model.insertRow(provider.Result{Title: "Example 1080p", Provider: "alpha", Seeders: 10})
	model.filterIn.SetValue("size:huge")
	model.refreshFilter()
	if model.filterErr == "" {
		t.Fatal("expected filter error")
	}
	if len(model.visible) != 0 {
		t.Fatalf("visible = %v, want none", model.visible)
	}
}
