package autopilot

import (
	"testing"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

// hashMagnet builds a magnet with a deterministic infohash from a 40-char hex.
func hashMagnet(hex40 string) string {
	return "magnet:?xt=urn:btih:" + hex40
}

func res(title string, seeders int, magnet string) provider.Result {
	return provider.Result{Title: title, Seeders: seeders, Magnet: magnet, Provider: "test"}
}

func titles(picks []Pick) []string {
	out := make([]string, len(picks))
	for i, p := range picks {
		out[i] = p.Result.Title
	}
	return out
}

func contains(picks []Pick, substr string) bool {
	for _, p := range picks {
		if p.Result.Title == substr {
			return true
		}
	}
	return false
}

func TestSelectPrefersResolution(t *testing.T) {
	results := []provider.Result{
		res("Inception 2010 720p WEB", 500, hashMagnet("1111111111111111111111111111111111111111")),
		res("Inception 2010 1080p WEB", 300, hashMagnet("2222222222222222222222222222222222222222")),
	}
	in := Intent{Query: "inception", WantRes: rank.Res1080, MinSeeders: 1, Max: 10}
	picks := Select(results, in, rank.DefaultWeights(), nil)
	if len(picks) != 1 {
		t.Fatalf("want 1 pick (best resolution of one content), got %d: %v", len(picks), titles(picks))
	}
	if picks[0].Tags.Resolution != rank.Res1080 {
		t.Errorf("expected 1080p pick, got %s", picks[0].Tags.Resolution)
	}
}

func TestSelectResolutionFallback(t *testing.T) {
	// No 1080p available → keep the best of what exists.
	results := []provider.Result{
		res("Old Movie 1999 720p BluRay", 200, hashMagnet("3333333333333333333333333333333333333333")),
		res("Old Movie 1999 480p DVD", 50, hashMagnet("4444444444444444444444444444444444444444")),
	}
	in := Intent{Query: "old movie", WantRes: rank.Res1080, MinSeeders: 1, Max: 10}
	picks := Select(results, in, rank.DefaultWeights(), nil)
	if len(picks) != 1 || picks[0].Tags.Resolution != rank.Res720 {
		t.Fatalf("fallback should keep best available (720p), got %v", titles(picks))
	}
}

func TestSelectSeasonPackBeatsEpisodes(t *testing.T) {
	results := []provider.Result{
		res("Breaking Bad S01E01 1080p WEB", 100, hashMagnet("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")),
		res("Breaking Bad S01E02 1080p WEB", 90, hashMagnet("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")),
		res("Breaking Bad S01 Complete 1080p WEB", 400, hashMagnet("cccccccccccccccccccccccccccccccccccccccc")),
	}
	in := Intent{Query: "breaking bad", WantRes: rank.Res1080, Season: 1, MinSeeders: 1, Max: 10}
	picks := Select(results, in, rank.DefaultWeights(), nil)
	if len(picks) != 1 {
		t.Fatalf("want 1 pack pick for season 1, got %d: %v", len(picks), titles(picks))
	}
	if !contains(picks, "Breaking Bad S01 Complete 1080p WEB") {
		t.Errorf("expected the S01 pack, got %v", titles(picks))
	}
}

func TestSelectAllSeasonsPrefersCompletePack(t *testing.T) {
	results := []provider.Result{
		res("The Wire S01 1080p WEB", 300, hashMagnet("1010101010101010101010101010101010101010")),
		res("The Wire S02 1080p WEB", 280, hashMagnet("2020202020202020202020202020202020202020")),
		res("The Wire S01-S05 Complete 1080p BluRay", 900, hashMagnet("3030303030303030303030303030303030303030")),
	}
	in := Intent{Query: "the wire", WantRes: rank.Res1080, AllSeasons: true, MinSeeders: 1, Max: 10}
	picks := Select(results, in, rank.DefaultWeights(), nil)
	if len(picks) != 1 || !contains(picks, "The Wire S01-S05 Complete 1080p BluRay") {
		t.Fatalf("all-seasons should collapse to the complete pack, got %v", titles(picks))
	}
}

func TestSelectSkipsKnownHashes(t *testing.T) {
	magnet := hashMagnet("dddddddddddddddddddddddddddddddddddddddd")
	results := []provider.Result{res("Some Movie 2020 1080p WEB", 300, magnet)}
	m, _ := metainfo.ParseMagnetUri(magnet)
	known := map[metainfo.Hash]bool{m.InfoHash: true}

	in := Intent{Query: "some movie", MinSeeders: 1, Max: 10}
	picks := Select(results, in, rank.DefaultWeights(), known)
	if len(picks) != 0 {
		t.Errorf("known hash should be skipped, got %v", titles(picks))
	}
}

func TestSelectSeederFloorAndCap(t *testing.T) {
	results := []provider.Result{
		res("Movie A 2020 1080p", 3, hashMagnet("a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1")), // below floor
		res("Movie B 2020 1080p", 50, hashMagnet("b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1")),
		res("Movie C 2020 1080p", 60, hashMagnet("c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1c1")),
		res("Movie D 2020 1080p", 70, hashMagnet("d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1")),
	}
	in := Intent{Query: "movie", MinSeeders: 5, Max: 2}
	picks := Select(results, in, rank.DefaultWeights(), nil)
	if len(picks) != 2 {
		t.Fatalf("cap should limit to 2, got %d: %v", len(picks), titles(picks))
	}
	if contains(picks, "Movie A 2020 1080p") {
		t.Error("Movie A is below the seeder floor and must be excluded")
	}
}

func TestBuildPlanExplainsSafetyRejections(t *testing.T) {
	knownMagnet := hashMagnet("eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	knownMeta, _ := metainfo.ParseMagnetUri(knownMagnet)
	results := []provider.Result{
		{Title: "Good Movie 1080p", Provider: "test", Category: "Movies/HD", Seeders: 50, SizeBytes: 2 << 30, Magnet: hashMagnet("1111111111111111111111111111111111111111")},
		{Title: "Huge Movie 1080p", Provider: "test", Category: "Movies/HD", Seeders: 50, SizeBytes: 20 << 30, Magnet: hashMagnet("2222222222222222222222222222222222222222")},
		{Title: "Mystery Movie 1080p", Provider: "test", Category: "Movies/HD", Seeders: 50, Magnet: hashMagnet("3333333333333333333333333333333333333333")},
		{Title: "Quiet Movie 1080p", Provider: "test", Category: "Movies/HD", Seeders: 2, SizeBytes: 1 << 30, Magnet: hashMagnet("4444444444444444444444444444444444444444")},
		{Title: "Wrong Category 1080p", Provider: "test", Category: "Anime", Seeders: 50, SizeBytes: 1 << 30, Magnet: hashMagnet("5555555555555555555555555555555555555555")},
		{Title: "Known Movie 1080p", Provider: "test", Category: "Movies", Seeders: 50, SizeBytes: 1 << 30, Magnet: knownMagnet},
	}
	in := Intent{Query: "movie", MinSeeders: 5, Max: 10, MaxSizeBytes: 8 << 30, Categories: []string{"movies"}}
	plan := BuildPlan(results, in, rank.DefaultWeights(), map[metainfo.Hash]bool{knownMeta.InfoHash: true})
	if len(plan.Picks) != 1 || plan.Picks[0].Result.Title != "Good Movie 1080p" {
		t.Fatalf("picks = %v, want only Good Movie", titles(plan.Picks))
	}
	for reason, want := range map[string]int{
		"over size limit": 1, "size unknown": 1, "below seeder minimum": 1,
		"category not allowed": 1, "already queued": 1,
	} {
		if got := plan.Rejected[reason]; got != want {
			t.Errorf("rejected[%q] = %d, want %d", reason, got, want)
		}
	}
	if plan.TotalBytes != 2<<30 {
		t.Fatalf("TotalBytes = %d, want %d", plan.TotalBytes, int64(2<<30))
	}
}

func TestCategoryAllowedMatchesSegmentsNotSubstrings(t *testing.T) {
	cases := []struct {
		category string
		allowed  []string
		want     bool
	}{
		{"Movies", []string{"movies"}, true},
		{"movie", []string{"movies"}, true}, // 1337x singular icon label
		{"Movies > HD", []string{"movies"}, true},
		{"movies/hd", []string{"movies"}, true},
		{"Anime - English-translated", []string{"anime"}, true},
		{"XXX Movies", []string{"movies"}, false},
		{"XXX", []string{"movies", "anime"}, false},
		{"Anime Music Video", []string{"anime"}, false},
		{"", []string{"movies"}, false},
		{"Movies", nil, false},
	}
	for _, tc := range cases {
		if got := categoryAllowed(tc.category, tc.allowed); got != tc.want {
			t.Errorf("categoryAllowed(%q, %v) = %v, want %v", tc.category, tc.allowed, got, tc.want)
		}
	}
}
