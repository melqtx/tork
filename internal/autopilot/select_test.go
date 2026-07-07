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
