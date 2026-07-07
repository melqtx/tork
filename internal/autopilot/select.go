package autopilot

import (
	"fmt"
	"sort"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/rank"
)

// Pick is a chosen download with the reasoning behind it.
type Pick struct {
	Result provider.Result
	Tags   rank.Tags
	Score  float64
	Reason string
}

// Select turns raw search results into a deduplicated set of best-choice
// downloads honoring the intent. `known` holds infohashes already present so
// autopilot never re-queues an existing download. Pure and deterministic.
func Select(results []provider.Result, in Intent, w rank.Weights, known map[metainfo.Hash]bool) []Pick {
	maxDownloads := in.Max
	if maxDownloads <= 0 {
		maxDownloads = 10
	}

	// 1. score + seeder floor
	var cands []Pick
	for _, r := range results {
		if r.Seeders < in.MinSeeders {
			continue
		}
		t := rank.Parse(r.Title)
		cands = append(cands, Pick{Result: r, Tags: t, Score: rank.Score(r, t, w)})
	}

	// 2. best candidate per source group (title + season + resolution)
	best := map[string]Pick{}
	for _, c := range cands {
		k := rank.GroupKey(c.Result.Title, c.Tags)
		if e, ok := best[k]; !ok || c.Score > e.Score {
			best[k] = c
		}
	}
	groups := make([]Pick, 0, len(best))
	for _, g := range best {
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Score > groups[j].Score })

	// 3. resolution preference: within each content, drop non-matching
	//    resolutions unless nothing matches (fallback keeps the best available)
	if in.WantRes != rank.ResUnknown {
		groups = preferResolution(groups, in.WantRes)
	}

	// 4. selection
	sel := &selector{maxDownloads: maxDownloads, known: known}
	if in.AllSeasons || in.Season > 0 {
		return sel.seasons(groups, in)
	}
	return sel.byContent(groups, in)
}

type selector struct {
	maxDownloads int
	known        map[metainfo.Hash]bool
	usedHash     map[metainfo.Hash]bool
	picks        []Pick
}

func (s *selector) add(p Pick, reason string) {
	if s.usedHash == nil {
		s.usedHash = map[metainfo.Hash]bool{}
	}
	if h, ok := infoHash(p.Result); ok {
		if s.known[h] || s.usedHash[h] {
			return
		}
		s.usedHash[h] = true
	}
	p.Reason = reason
	s.picks = append(s.picks, p)
}

func (s *selector) full() bool { return len(s.picks) >= s.maxDownloads }

// byContent picks the single best group per distinct content (one per movie).
func (s *selector) byContent(groups []Pick, in Intent) []Pick {
	seen := map[string]bool{}
	for _, g := range groups {
		if s.full() {
			break
		}
		ck := rank.ContentKey(g.Result.Title, g.Tags)
		if seen[ck] {
			continue
		}
		seen[ck] = true
		s.add(g, reasonFor(g, in))
	}
	return s.picks
}

// seasons covers whole series/seasons, preferring packs over episodes.
func (s *selector) seasons(groups []Pick, in Intent) []Pick {
	// A complete/range pack covers everything: take the best one and stop.
	if in.AllSeasons {
		for _, g := range groups {
			if g.Tags.Complete || g.Tags.SeasonEnd > 0 {
				s.add(g, reasonFor(g, in))
				if len(s.picks) > 0 {
					return s.picks
				}
			}
		}
	}

	// Otherwise, best pack (else best episode) per season.
	coveredPack := map[int]bool{}
	// first pass: season packs
	for _, g := range groups {
		if s.full() {
			return s.picks
		}
		if !g.Tags.IsPack() || g.Tags.Season == 0 {
			continue
		}
		if in.Season > 0 && !covers(g.Tags, in.Season) {
			continue
		}
		if coveredPack[g.Tags.Season] {
			continue
		}
		coveredPack[g.Tags.Season] = true
		s.add(g, reasonFor(g, in))
	}
	// second pass: episodes for seasons still uncovered
	coveredEp := map[int]bool{}
	for _, g := range groups {
		if s.full() {
			return s.picks
		}
		if g.Tags.Season == 0 || g.Tags.IsPack() {
			continue
		}
		if in.Season > 0 && g.Tags.Season != in.Season {
			continue
		}
		if coveredPack[g.Tags.Season] || coveredEp[g.Tags.Season] {
			continue
		}
		coveredEp[g.Tags.Season] = true
		s.add(g, reasonFor(g, in))
	}
	return s.picks
}

// covers reports whether a pack's season span includes season n.
func covers(t rank.Tags, n int) bool {
	if t.SeasonEnd > 0 {
		return t.Season <= n && n <= t.SeasonEnd
	}
	if t.Complete {
		return true
	}
	return t.Season == n
}

// preferResolution keeps, per content, only groups matching want when at least
// one does; otherwise the content's groups pass through untouched (fallback).
func preferResolution(groups []Pick, want rank.Resolution) []Pick {
	hasMatch := map[string]bool{}
	for _, g := range groups {
		if g.Tags.Resolution == want {
			hasMatch[rank.ContentKey(g.Result.Title, g.Tags)] = true
		}
	}
	out := groups[:0]
	for _, g := range groups {
		ck := rank.ContentKey(g.Result.Title, g.Tags)
		if hasMatch[ck] && g.Tags.Resolution != want {
			continue
		}
		out = append(out, g)
	}
	return out
}

func reasonFor(p Pick, in Intent) string {
	switch {
	case p.Tags.SeasonEnd > 0:
		return fmt.Sprintf("pack S%02d-S%02d, %d seeders", p.Tags.Season, p.Tags.SeasonEnd, p.Result.Seeders)
	case p.Tags.Complete:
		return fmt.Sprintf("complete pack, %d seeders", p.Result.Seeders)
	case p.Tags.IsPack():
		return fmt.Sprintf("S%02d pack, %d seeders", p.Tags.Season, p.Result.Seeders)
	}
	res := p.Tags.Resolution.String()
	if res == "" {
		res = "best available"
	}
	if in.WantRes != rank.ResUnknown && p.Tags.Resolution != in.WantRes {
		return fmt.Sprintf("fallback %s (no %s), %d seeders", res, in.WantRes, p.Result.Seeders)
	}
	return fmt.Sprintf("best %s, %d seeders", res, p.Result.Seeders)
}

func infoHash(r provider.Result) (metainfo.Hash, bool) {
	if r.Magnet == "" {
		return metainfo.Hash{}, false
	}
	m, err := metainfo.ParseMagnetUri(r.Magnet)
	if err != nil {
		return metainfo.Hash{}, false
	}
	return m.InfoHash, true
}
