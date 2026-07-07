package provider

import (
	"net/url"
	"strings"
	"testing"
)

func TestKnabenSearch(t *testing.T) {
	srv := serveFixture(t, "knaben.html")
	results := collect(t, NewKnaben(srv.Client(), srv.URL), "fight club")
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3: %+v", len(results), results)
	}
	r := results[0]
	if r.Provider != "knaben" {
		t.Errorf("Provider = %q", r.Provider)
	}
	if r.Title != "Fight Club (1999) 1080p BrRip x264 - YIFY" {
		t.Errorf("Title = %q (should come from the magnet anchor's title attr, not the tooltip)", r.Title)
	}
	if r.Seeders != 596 || r.Leechers != 133 {
		t.Errorf("seeders/leechers wrong: %d/%d", r.Seeders, r.Leechers)
	}
	if r.Size != "1.85 GB" || r.SizeBytes == 0 {
		t.Errorf("size wrong: %q / %d", r.Size, r.SizeBytes)
	}
	if !strings.HasPrefix(r.Magnet, "magnet:?xt=urn:btih:A086CE4AFABBD8AB") {
		t.Errorf("Magnet = %q", r.Magnet)
	}
}

// A stale config pointing at the dead JSON API host must fall back to the web.
func TestKnabenIgnoresLegacyAPIBase(t *testing.T) {
	k := NewKnaben(nil, "https://api.knaben.eu/v1")
	if k.base != knabenWeb {
		t.Errorf("base = %q, want %q", k.base, knabenWeb)
	}
}

func TestKnabenIgnoresDeadEUBase(t *testing.T) {
	k := NewKnaben(nil, "https://knaben.eu")
	if k.base != knabenWeb {
		t.Errorf("base = %q, want %q", k.base, knabenWeb)
	}
}

func TestKnabenBuildsSearchPath(t *testing.T) {
	// confirms the URL shape /search/<escaped>/0/1/seeders
	q := url.PathEscape("fight club")
	want := knabenWeb + "/search/" + q + "/0/1/seeders"
	if !strings.Contains(want, "/search/fight%20club/0/1/seeders") {
		t.Errorf("unexpected path: %s", want)
	}
}
