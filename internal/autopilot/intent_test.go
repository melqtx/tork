package autopilot

import (
	"testing"

	"github.com/melqtx/tork/internal/rank"
)

func TestParseIntent(t *testing.T) {
	cases := []struct {
		raw        string
		query      string
		res        rank.Resolution
		allSeasons bool
		season     int
	}{
		{"download all breaking bad seasons 1080p", "breaking bad", rank.Res1080, true, 0},
		{"inception 2010 2160p", "inception 2010", rank.Res2160, false, 0},
		{"the office season 3", "office", rank.ResUnknown, false, 3},
		{"get severance complete 4k", "severance", rank.Res2160, true, 0},
		{"attack on titan", "attack on titan", rank.ResUnknown, false, 0},
	}
	for _, c := range cases {
		in := ParseIntent(c.raw)
		if in.Query != c.query {
			t.Errorf("ParseIntent(%q).Query = %q, want %q", c.raw, in.Query, c.query)
		}
		if in.WantRes != c.res {
			t.Errorf("ParseIntent(%q).WantRes = %v, want %v", c.raw, in.WantRes, c.res)
		}
		if in.AllSeasons != c.allSeasons {
			t.Errorf("ParseIntent(%q).AllSeasons = %v, want %v", c.raw, in.AllSeasons, c.allSeasons)
		}
		if in.Season != c.season {
			t.Errorf("ParseIntent(%q).Season = %d, want %d", c.raw, in.Season, c.season)
		}
	}
}

func TestParseIntentExtractsNaturalSizeLimit(t *testing.T) {
	in := ParseIntent("grab dune 2024 2160p under 12.5GB")
	if in.Query != "dune 2024" {
		t.Fatalf("Query = %q, want dune 2024", in.Query)
	}
	want := int64(12.5 * (1 << 30))
	if in.MaxSizeBytes != want {
		t.Fatalf("MaxSizeBytes = %d, want %d", in.MaxSizeBytes, want)
	}
}

func TestParseSizeLimit(t *testing.T) {
	got, err := ParseSizeLimit("1.5 GiB")
	if err != nil || got != int64(1.5*(1<<30)) {
		t.Fatalf("ParseSizeLimit = %d, %v", got, err)
	}
	if _, err := ParseSizeLimit("lots"); err == nil {
		t.Fatal("ParseSizeLimit accepted a non-size")
	}
	if _, err := ParseSizeLimit("999999999999999TB"); err == nil {
		t.Fatal("ParseSizeLimit accepted an overflowing size")
	}
}
