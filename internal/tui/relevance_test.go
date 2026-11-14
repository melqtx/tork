package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

func rankingFixture(query string, results ...provider.Result) resultsModel {
	r := newResultsModel(rank.DefaultWeights())
	r.query = query
	for i, result := range results {
		result.Magnet = provider.BuildMagnet(fmt.Sprintf("%040x", i+1), result.Title, nil)
		r.insertRow(result)
	}
	r.refreshFilter()
	return r
}

func TestSearchRankingSpiderMan(t *testing.T) {
	r := rankingFixture("spiderman",
		provider.Result{Title: "Spiderman 2002 REMASTERED", Seeders: 60},
		provider.Result{Title: "Spiderman Web of Shadows-RELOADED", Seeders: 8},
		provider.Result{Title: "The Amazing Spiderman 2012 DVDRip XviD IMAGiNE", Seeders: 17255},
		provider.Result{Title: "Spider-Man 2002 1080p BluRay x264", Seeders: 500},
	)
	want := []string{"Spider-Man 2002 1080p BluRay x264", "Spiderman 2002 REMASTERED", "The Amazing Spiderman 2012 DVDRip XviD IMAGiNE", "Spiderman Web of Shadows-RELOADED"}
	for i, title := range want {
		if got := r.rows[r.visible[i]].res.Title; got != title {
			t.Fatalf("position %d: %q, want %q", i, got, title)
		}
	}
	for _, query := range []string{"spiderman", "spider man", "spider-man"} {
		if got := assessQuery(query, r.rows[0]).title; got != 100 {
			t.Fatalf("%q failed to match hyphenated title: %d", query, got)
		}
	}
	if assessQuery("man", r.newRow(provider.Result{Title: "Human 2020"})).title != 0 {
		t.Fatal("substring mistaken for a title word")
	}
}

func TestSearchRankingAvailabilityAndConstraints(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		results     []provider.Result
		want        string
	}{
		{"live related before reported-zero exact", "spiderman", []provider.Result{{Title: "Spiderman 2002", Seeders: 0}, {Title: "The Amazing Spiderman 2012", Seeders: 1000}}, "The Amazing Spiderman 2012"},
		{"unknown before reported zero", "spiderman", []provider.Result{{Title: "Spiderman 2002", Seeders: 0}, {Title: "Spiderman 2002 1080p", SeedersUnknown: true}}, "Spiderman 2002 1080p"},
		{"related before unrelated popular", "spiderman", []provider.Result{{Title: "Spiderman 2002", Seeders: 0}, {Title: "Other Movie 2002", Seeders: 50000}}, "Spiderman 2002"},
		{"requested year before wrong year", "Dune 2021", []provider.Result{{Title: "Dune 1984 1080p", Seeders: 50000}, {Title: "Dune 2021 1080p", Seeders: 0}}, "Dune 2021 1080p"},
		{"requested episode before wrong episode", "Example S02E03", []provider.Result{{Title: "Example S02E04 1080p", Seeders: 50000}, {Title: "Example S02E03 1080p", Seeders: 5}}, "Example S02E03 1080p"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rankingFixture(tc.query, tc.results...)
			if r.rows[0].res.Title != tc.want {
				t.Fatalf("top=%q want=%q", r.rows[0].res.Title, tc.want)
			}
		})
	}
}

func TestSearchSortSurvivesFiltersAndViews(t *testing.T) {
	for _, mode := range []sortMode{sortScore, sortSeeders, sortSize} {
		t.Run(mode.String(), func(t *testing.T) {
			r := rankingFixture("Dune 2021", provider.Result{Title: "Dune 2021 1080p BluRay", Seeders: 1, SizeBytes: 1 << 30}, provider.Result{Title: "Children of Dune 2021 1080p BluRay", Seeders: 10000, SizeBytes: 8 << 30})
			r.sort = mode
			r.filterIn.SetValue("dune res:1080p")
			want := "Dune 2021 1080p BluRay"
			if mode != sortScore {
				want = "Children of Dune 2021 1080p BluRay"
			}
			for _, browser := range []bool{true, false} {
				r.browser = browser
				r.curated = true
				r.grouped = true
				r.refreshFilter()
				if got := r.rows[r.visible[0]].res.Title; got != want {
					t.Fatalf("browser=%v top=%q want=%q", browser, got, want)
				}
				if got := pickTitles(&r)[0]; got != want {
					t.Fatalf("picks top=%q want=%q", got, want)
				}
				if got := r.rows[r.groups[0].rowIdx[0]].res.Title; got != want {
					t.Fatalf("groups top=%q want=%q", got, want)
				}
			}
		})
	}
}

func TestUnknownSeedersStayVisibleWithoutBecomingZero(t *testing.T) {
	r := rankingFixture("Example", provider.Result{Title: "Example 1080p", SeedersUnknown: true}, provider.Result{Title: "Example 720p", Seeders: 0})
	r.hideDead = true
	r.refreshFilter()
	if len(r.visible) != 1 || r.rows[r.visible[0]].res.KnownSeeders() {
		t.Fatal("zero-seeder toggle hid unknown availability")
	}
	row := r.rows[r.visible[0]]
	if row.noisy || !strings.Contains(renderReleaseRow(row, 100), "seeds ?") || !strings.Contains(r.rankingReason(row), "availability unknown") {
		t.Fatal("unknown count displayed or explained as zero")
	}
	for _, query := range []string{"seeders:0", "seeders:>0", "-seeders:>0"} {
		if parseResultFilter(query).matches(row) {
			t.Fatalf("unknown count matched numeric filter %q", query)
		}
	}
}

func TestMergePreservesReleaseMetadataAndReportsInAnyOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		r := newResultsModel(rank.DefaultWeights())
		r.query = "Dune 2021"
		results := []provider.Result{{Title: "Dune 2021", Provider: "alpha", Seeders: 1000, Leechers: 20}, {Title: "Dune 2021 1080p BluRay x265", Provider: "beta", Seeders: 10, Leechers: 2}}
		if reverse {
			results[0], results[1] = results[1], results[0]
		}
		for _, result := range results {
			result.Magnet = provider.BuildMagnet(rowHash, "Dune", nil)
			r.insertRow(result)
		}
		r.refreshFilter()
		row := r.rows[0]
		if len(r.rows) != 1 || row.tags.Resolution != rank.Res1080 || row.tags.Codec != "x265" || row.res.Seeders != 1000 || row.res.Leechers != 20 || len(row.res.Observations) != 2 {
			t.Fatalf("reverse=%v merged result lost metadata/reports: %+v", reverse, row)
		}
	}
}

func TestBrowseShowsQualityAndActualRankingReason(t *testing.T) {
	a := navigationApp()
	a.screen = screenResults
	a.width = 140
	a.height = 30
	a.results = rankingFixture("spiderman", provider.Result{Title: "Spider-Man 2002 1080p BluRay x265", Provider: "alpha", Seeders: 50, SizeBytes: 2 << 30})
	a.results.browser = true
	for _, want := range []string{"1080p BluRay x265", "exact title match", "50 reported seeds", "alpha"} {
		if !strings.Contains(a.View(), want) {
			t.Fatalf("browse missing %q", want)
		}
	}
	a.results.sort = sortSeeders
	a.results.refreshFilter()
	if !strings.Contains(a.View(), "sorted by reported seeders") {
		t.Fatal("explanation ignored explicit sort")
	}
}

func TestSelectedSourceShowsOriginalReportAndFetchTime(t *testing.T) {
	a := navigationApp()
	a.screen = screenResults
	a.width = 140
	a.height = 30
	a.results = rankingFixture("Example", provider.Result{Title: "Example 1080p", Provider: "alpha", Seeders: 100, Leechers: 3})
	a.results.rows[0].res = a.results.rows[0].res.WithObservation(time.Now())
	a.results.sourcePanel = true
	a.agg = aggregator.New([]provider.Provider{resultStatusProvider("alpha")}, 0, 0)
	for _, want := range []string{"Selected release · S 100 · L 3", "Fetched", "index refresh time unknown"} {
		if !strings.Contains(a.View(), want) {
			t.Fatalf("source report missing %q", want)
		}
	}
}
