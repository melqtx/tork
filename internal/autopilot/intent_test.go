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
