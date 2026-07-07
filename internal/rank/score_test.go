package rank

import (
	"testing"

	"github.com/melqtx/tork/internal/provider"
)

func scoreOf(title string, seeders, leechers int, prov string, trusted bool) float64 {
	r := provider.Result{Title: title, Seeders: seeders, Leechers: leechers, Provider: prov, Trusted: trusted}
	return Score(r, Parse(title), DefaultWeights())
}

func TestQualityBeatsRawSeeders(t *testing.T) {
	// A 1080p WEB-DL with modest seeders must outrank a CAM with a huge swarm.
	good := scoreOf("Movie 2023 1080p WEB-DL x264", 100, 10, "1337x", false)
	cam := scoreOf("Movie 2023 720p HDCAM x264", 2000, 50, "1337x", false)
	if good <= cam {
		t.Errorf("1080p WEB-DL (%.1f) should beat CAM (%.1f)", good, cam)
	}
}

func TestDeadTorrentSinks(t *testing.T) {
	dead := scoreOf("Movie 2023 2160p REMUX", 0, 0, "1337x", false)
	alive := scoreOf("Movie 2023 480p", 3, 1, "1337x", false)
	if dead >= alive {
		t.Errorf("0-seeder torrent (%.1f) must sink below a live one (%.1f)", dead, alive)
	}
}

func TestHealthBreaksTies(t *testing.T) {
	// Same quality and seeders; the healthier swarm (fewer leechers) wins.
	healthy := scoreOf("Movie 2023 1080p WEB-DL", 100, 5, "1337x", false)
	leechy := scoreOf("Movie 2023 1080p WEB-DL", 100, 400, "1337x", false)
	if healthy <= leechy {
		t.Errorf("healthier swarm (%.1f) should beat leech-heavy (%.1f)", healthy, leechy)
	}
}

func TestTrustedBoost(t *testing.T) {
	trusted := scoreOf("Anime 01 1080p", 100, 10, "nyaa", true)
	untrusted := scoreOf("Anime 01 1080p", 100, 10, "nyaa", false)
	if trusted <= untrusted {
		t.Errorf("trusted (%.1f) should beat untrusted (%.1f)", trusted, untrusted)
	}
}
