package tui

import (
	"strings"
	"testing"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

const (
	rowHash   = "0123456789abcdef0123456789abcdef01234567"
	otherHash = "89abcdef0123456789abcdef0123456789abcdef"
)

func newTestResults() resultsModel {
	r := newResultsModel(rank.DefaultWeights())
	r.query = "some release"
	return r
}

// TestInsertRowMergesSameTorrentAcrossProviders is the headline behavior: three
// indexes listing one torrent collapse to one row whose magnet announces to all
// three tracker sets.
func TestInsertRowMergesSameTorrentAcrossProviders(t *testing.T) {
	r := newTestResults()
	r.insertRow(provider.Result{
		Title: "Some Release", Provider: "knaben", Seeders: 12,
		Magnet: provider.BuildMagnet(rowHash, "r", []string{"udp://one:1/announce"}),
	})
	r.insertRow(provider.Result{
		Title: "Some.Release.2024.1080p.BluRay.x264-GRP", Provider: "yts", Seeders: 340,
		Magnet: provider.BuildMagnet(rowHash, "r", []string{"udp://two:2/announce"}),
	})
	r.insertRow(provider.Result{
		Title: "Some Release 1080p", Provider: "1337x", Seeders: 5,
		Magnet: provider.BuildMagnet(rowHash, "r", []string{"udp://three:3/announce"}),
	})
	r.refreshFilter()

	if len(r.rows) != 1 {
		t.Fatalf("%d rows, want 1 (one torrent, three listings)", len(r.rows))
	}
	row := r.rows[0]
	if n := len(row.res.Trackers()); n != 3 {
		t.Errorf("%d trackers on the merged magnet, want 3 (the union)", n)
	}
	if row.res.Seeders != 340 {
		t.Errorf("Seeders = %d, want 340 (best of the three scrapes)", row.res.Seeders)
	}
	// The fullest release name wins, so the tag parser and group label read the
	// codec and release group instead of a tidied-up listing's short title.
	if row.res.Provider != "yts" {
		t.Errorf("keeper = %q, want yts (best-scoring listing names the release)", row.res.Provider)
	}
	if want := []string{"1337x", "knaben"}; strings.Join(row.res.AlsoOn, ",") != strings.Join(want, ",") {
		t.Errorf("AlsoOn = %v, want %v", row.res.AlsoOn, want)
	}
	if r.merged != 2 {
		t.Errorf("merged counter = %d, want 2", r.merged)
	}
	if row.tags.Codec == "" || rank.ReleaseGroup(row.res.Title) == "" {
		t.Errorf("merged row lost its parsed tags: %+v", row.tags)
	}
}

// TestMergeLiftsRowIntoItsNewRank guards the re-sort: a row merged up to a much
// bigger swarm must not be left sitting below rows it now outranks.
func TestMergeLiftsRowIntoItsNewRank(t *testing.T) {
	r := newTestResults()
	r.insertRow(provider.Result{
		Title: "Some Release 1080p", Provider: "knaben", Seeders: 500,
		Magnet: provider.BuildMagnet(otherHash, "big", nil),
	})
	r.insertRow(provider.Result{
		Title: "Some Release 1080p", Provider: "yts", Seeders: 10,
		Magnet: provider.BuildMagnet(rowHash, "small", []string{"udp://one:1/announce"}),
	})
	r.refreshFilter()
	if r.rows[0].res.Seeders != 500 {
		t.Fatalf("setup: rows not sorted best-first, got %d on top", r.rows[0].res.Seeders)
	}

	r.insertRow(provider.Result{
		Title: "Some Release 1080p", Provider: "1337x", Seeders: 900,
		Magnet: provider.BuildMagnet(rowHash, "small", []string{"udp://two:2/announce"}),
	})
	r.refreshFilter()

	if len(r.rows) != 2 {
		t.Fatalf("%d rows, want 2", len(r.rows))
	}
	if r.rows[0].res.Seeders != 900 {
		t.Fatalf("top row has %d seeders, want the merged 900-seeder row lifted to the top",
			r.rows[0].res.Seeders)
	}
	if r.bestIdx != 0 {
		t.Errorf("bestIdx = %d, want 0", r.bestIdx)
	}
}

// TestInsertRowKeepsDistinctTorrentsApart makes sure merging only ever folds
// rows that are provably the same bytes.
func TestInsertRowKeepsDistinctTorrentsApart(t *testing.T) {
	r := newTestResults()
	r.insertRow(provider.Result{Title: "Some Release 1080p", Provider: "knaben", Seeders: 9,
		Magnet: provider.BuildMagnet(rowHash, "a", nil)})
	r.insertRow(provider.Result{Title: "Some Release 1080p", Provider: "yts", Seeders: 9,
		Magnet: provider.BuildMagnet(otherHash, "b", nil)})
	// Identical titles, but no magnet to compare: a detail-page row cannot be
	// proven to be the same torrent without a network round trip, so it stays.
	r.insertRow(provider.Result{Title: "Some Release 1080p", Provider: "1337x", Seeders: 9,
		DetailURL: "https://example.org/t/1"})
	r.insertRow(provider.Result{Title: "Some Release 1080p", Provider: "eztv", Seeders: 9,
		DetailURL: "https://example.org/t/2"})
	r.refreshFilter()

	if len(r.rows) != 4 {
		t.Fatalf("%d rows, want 4 (two distinct hashes, two unresolvable listings)", len(r.rows))
	}
	if r.merged != 0 {
		t.Errorf("merged = %d, want 0", r.merged)
	}
}

// TestInsertRowStillDropsRepeatListings keeps the original per-provider retry
// dedupe working alongside the cross-provider merge.
func TestInsertRowStillDropsRepeatListings(t *testing.T) {
	r := newTestResults()
	res := provider.Result{Title: "Some Release 1080p", Provider: "knaben", Seeders: 9,
		Magnet: provider.BuildMagnet(rowHash, "a", nil)}
	r.insertRow(res)
	r.insertRow(res)
	r.refreshFilter()

	if len(r.rows) != 1 {
		t.Fatalf("%d rows, want 1", len(r.rows))
	}
	if r.merged != 0 {
		t.Errorf("merged = %d, want 0 (a retry is not a second source)", r.merged)
	}
	if len(r.rows[0].res.AlsoOn) != 0 {
		t.Errorf("AlsoOn = %v, want empty (same provider twice)", r.rows[0].res.AlsoOn)
	}
}

func TestStatusLineReportsMerges(t *testing.T) {
	r := newTestResults()
	r.merged = 3
	r.rows = []scoredRow{{res: provider.Result{Title: "x"}}}
	if got := r.statusLine(aggregator.New(nil, 0, 0)); !strings.Contains(got, "3 merged") {
		t.Fatalf("status line %q does not report merged listings", got)
	}
}
