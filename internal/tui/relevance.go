package tui

import (
	"fmt"
	"strings"

	"github.com/melqtx/tork/internal/rank"
)

type queryAssessment struct {
	title, constraints int
	reasons            []string
}

// Match punctuation/spacing variants at whole-title or whole-word-sequence
// boundaries. This affects search ranking only, never torrent deduplication.
func titleKey(s string) string { return strings.ReplaceAll(s, " ", "") }
func containsTitleWords(title, query string) bool {
	words := strings.Fields(title)
	want := titleKey(query)
	for start := range words {
		candidate := ""
		for _, word := range words[start:] {
			candidate += word
			if candidate == want {
				return true
			}
			if len(candidate) >= len(want) {
				break
			}
		}
	}
	return false
}

func assessQuery(query string, row scoredRow) queryAssessment {
	q, year := rank.SplitTitle(query)
	title, gotYear := rank.SplitTitle(row.res.Title)
	m := queryAssessment{}
	switch {
	case q == "":
	case titleKey(q) == titleKey(title):
		m.title = 100
		m.reasons = append(m.reasons, "exact title match")
	case containsTitleWords(title, q):
		m.title = 60
		m.reasons = append(m.reasons, "title contains query")
	default:
		words, hits := strings.Fields(q), 0
		for _, word := range words {
			if containsTitleWords(title, word) {
				hits++
			}
		}
		if len(words) > 0 {
			m.title = 60 * hits / len(words)
		}
		if m.title > 0 {
			m.reasons = append(m.reasons, "partial title match")
		} else {
			m.reasons = append(m.reasons, "title match uncertain")
		}
	}
	check := func(label string, requested, known, matches bool) {
		if !requested {
			return
		}
		switch {
		case matches:
			m.constraints += 40
			m.reasons = append(m.reasons, label+" matches")
		case !known:
			m.constraints -= 20
			m.reasons = append(m.reasons, label+" unlisted")
		default:
			m.constraints -= 80
			m.reasons = append(m.reasons, label+" differs")
		}
	}
	check("year", year != "", gotYear != "", year != "" && year == gotYear)
	t := rank.Parse(query)
	check("season", t.Season > 0, row.tags.Season > 0, t.Season > 0 && (row.tags.Season == t.Season || row.tags.Season <= t.Season && row.tags.SeasonEnd >= t.Season))
	check("episode", t.Episode > 0, row.tags.Episode > 0, t.Episode > 0 && row.tags.Episode == t.Episode)
	return m
}

// Related, constraint-compatible releases with reported availability precede
// unavailable exact matches. Unrelated popular uploads stay below matches.
func availability(row scoredRow) int {
	if row.res.KnownSeeders() && row.res.Seeders <= 0 {
		return 0
	}
	if row.tags.Source == rank.SrcCam {
		return 1
	}
	if !row.res.KnownSeeders() {
		return 2
	}
	return 3
}

func (r *resultsModel) relevanceBetter(a, b scoredRow) bool {
	am, bm := r.assessment(a), r.assessment(b)
	if (am.title > 0) != (bm.title > 0) {
		return am.title > 0
	}
	if am.constraints != bm.constraints {
		return am.constraints > bm.constraints
	}
	if availability(a) != availability(b) {
		return availability(a) > availability(b)
	}
	if am.title != bm.title {
		return am.title > bm.title
	}
	switch r.pickPrefs.preference {
	case 1:
		if (a.res.SizeBytes > 0) != (b.res.SizeBytes > 0) {
			return a.res.SizeBytes > 0
		}
		if a.res.SizeBytes != b.res.SizeBytes {
			return a.res.SizeBytes < b.res.SizeBytes
		}
	case 2:
		if a.tags.Source != b.tags.Source {
			return a.tags.Source > b.tags.Source
		}
	}
	if a.score != b.score {
		return a.score > b.score
	}
	return rowIdentity(a) < rowIdentity(b)
}

func (r *resultsModel) assessment(row scoredRow) queryAssessment {
	if row.matchTitle == row.res.Title && row.matchQuery == r.query && row.matchTitle != "" {
		return row.match
	}
	return assessQuery(r.query, row)
}

func (r *resultsModel) rankingReason(row scoredRow) string {
	parts := []string{}
	if r.sort == sortSeeders {
		parts = append(parts, "sorted by reported seeders")
	}
	if r.sort == sortSize {
		parts = append(parts, "sorted by size, largest first")
	}
	parts = append(parts, r.assessment(row).reasons...)
	switch availability(row) {
	case 0:
		parts = append(parts, "zero seeders reported")
	case 1:
		parts = append(parts, "CAM source")
	case 2:
		parts = append(parts, "availability unknown")
	default:
		parts = append(parts, fmt.Sprintf("%d reported seeds", row.res.Seeders))
	}
	if r.sort == sortScore {
		switch r.pickPrefs.preference {
		case 1:
			parts = append(parts, "smaller files preferred")
		case 2:
			parts = append(parts, "source quality preferred")
		default:
			parts = append(parts, "swarm, quality and trust score")
		}
	}
	return strings.Join(parts, " · ")
}

// A sparse listing with a large swarm must not erase richer release metadata
// when two sources report the same infohash. Swarm observations merge separately.
func richerRelease(a, b scoredRow) bool {
	completeness := func(row scoredRow) int {
		n := 0
		_, year := rank.SplitTitle(row.res.Title)
		for _, present := range []bool{year != "", row.tags.Resolution != rank.ResUnknown,
			row.tags.Source != rank.SrcUnknown, row.tags.Codec != "", row.tags.Season > 0,
			row.tags.Episode > 0, row.tags.HDR, row.tags.DV} {
			if present {
				n++
			}
		}
		return n
	}
	if ac, bc := completeness(a), completeness(b); ac != bc {
		return ac > bc
	}
	if len(a.res.Title) != len(b.res.Title) {
		return len(a.res.Title) > len(b.res.Title)
	}
	return a.res.Provider+"\x00"+a.res.Title < b.res.Provider+"\x00"+b.res.Title
}
