// Package autopilot turns a natural-language intent into a set of best-choice
// downloads: parse the request, search, rank, pick, dedupe, queue.
package autopilot

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/melqtx/tork/internal/rank"
)

// Intent is the heuristic interpretation of an autopilot request.
type Intent struct {
	Query        string          // cleaned search terms
	WantRes      rank.Resolution // preferred resolution, ResUnknown if unspecified
	AllSeasons   bool            // "all seasons" / "complete" / "every season"
	Season       int             // specific season, 0 if none
	MinSeeders   int             // from config
	Max          int             // cap on downloads, from config
	MaxSizeBytes int64           // 0 means unlimited
	Categories   []string        // optional case-insensitive category allowlist
}

var (
	reFillerTokens = regexp.MustCompile(`(?i)\b(download|downloads|get|grab|fetch|please|want|need|the|a|an|of|for|me|some|entire|whole|full|all|every)\b`)
	reComplete     = regexp.MustCompile(`(?i)\b(complete[ ._]series|complete|collection)\b`)
	rePluralSeason = regexp.MustCompile(`(?i)\b(seasons|every[ ._]seasons?)\b`) // plural implies "all"
	reSeasonNum    = regexp.MustCompile(`(?i)\bseasons?[ ._]*(\d{1,2})\b`)
	reSeasonAny    = regexp.MustCompile(`(?i)\bseasons?([ ._]*\d{1,2})?\b`)
	reResToken     = regexp.MustCompile(`(?i)\b(2160p|4k|uhd|1080p|720p|480p)\b`)
	reMaxSize      = regexp.MustCompile(`(?i)\b(?:under|max(?:imum)?|up[ ._]+to)\s*(\d+(?:\.\d+)?)\s*(tb|tib|gb|gib|mb|mib)\b`)
	reWS           = regexp.MustCompile(`\s+`)
)

// ParseIntent extracts constraints and a clean query from a raw request like
// "download all breaking bad seasons 1080p".
func ParseIntent(raw string) Intent {
	in := Intent{}

	tags := rank.Parse(raw)
	in.WantRes = tags.Resolution
	if m := reMaxSize.FindStringSubmatch(raw); m != nil {
		in.MaxSizeBytes, _ = parseSizeParts(m[1], m[2])
	}

	// Detect season constraints on a resolution-stripped copy so tokens like
	// "1080p" can't be misread as a season number.
	work := reResToken.ReplaceAllString(raw, " ")
	if m := reSeasonNum.FindStringSubmatch(work); m != nil {
		in.Season = atoi(m[1])
	}
	if reComplete.MatchString(work) || rePluralSeason.MatchString(work) {
		in.AllSeasons = true
		in.Season = 0 // "all" overrides a specific season
	}

	// Build the search query by stripping constraint + filler tokens.
	q := reResToken.ReplaceAllString(raw, " ")
	q = reMaxSize.ReplaceAllString(q, " ")
	q = reComplete.ReplaceAllString(q, " ")
	q = reSeasonAny.ReplaceAllString(q, " ") // "season 3", "seasons", "season"
	q = reFillerTokens.ReplaceAllString(q, " ")
	q = reWS.ReplaceAllString(q, " ")
	in.Query = strings.TrimSpace(q)

	return in
}

// ParseSizeLimit accepts compact command-line limits such as 750MB, 8gb, or
// 1.5TiB. Torrent sizes are conventionally binary, so both GB and GiB use a
// 1024-based multiplier here.
func ParseSizeLimit(raw string) (int64, error) {
	m := regexp.MustCompile(`(?i)^\s*(\d+(?:\.\d+)?)\s*(tb|tib|gb|gib|mb|mib)\s*$`).FindStringSubmatch(raw)
	if m == nil {
		return 0, strconv.ErrSyntax
	}
	return parseSizeParts(m[1], m[2])
}

func parseSizeParts(number, unit string) (int64, error) {
	n, err := strconv.ParseFloat(number, 64)
	if err != nil || n <= 0 {
		return 0, strconv.ErrSyntax
	}
	var mul float64
	switch strings.ToLower(unit) {
	case "mb", "mib":
		mul = 1 << 20
	case "gb", "gib":
		mul = 1 << 30
	case "tb", "tib":
		mul = 1 << 40
	default:
		return 0, strconv.ErrSyntax
	}
	if n > float64(math.MaxInt64)/mul {
		return 0, strconv.ErrRange
	}
	return int64(n * mul), nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
