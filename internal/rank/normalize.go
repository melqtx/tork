package rank

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// A parenthesized year is content, not decoration - unwrap it before the
	// bracket stripper runs so "(2010)" survives as bare "2010".
	reParenYear = regexp.MustCompile(`\(((?:19|20)\d{2})\)`)
	reBracketed = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
	reExtension = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|mov|wmv|flv|m4v|ts|iso|torrent)$`)
	reSepChars  = regexp.MustCompile(`[._]+`)
	reMultiWS   = regexp.MustCompile(`\s+`)
	reNonAlnum  = regexp.MustCompile(`[^a-z0-9 ]+`)

	// Decoration tokens removed when normalizing (audio, HDR, bit-depth,
	// release junk). Resolution/source/codec/season are stripped separately.
	reNoise = regexp.MustCompile(`(?i)\b(` +
		`aac\d*|ac3|eac3\d*|dd\+|dd\d+|ddpa?\d*|dts|dtshd|truehd\d*|atmos|flac|opus|mp3|[2678]ch|` +
		`hdr10p(?:lus)?|hdr\d*|dolby|vision|dovi|dv|sdr|10bits?|8bit|hi10p|` +
		`multi|dual|audio|repack|proper|internal|limited|extended|remastered|` +
		`unrated|uncut|imax|hybrid|readnfo|dubbed|subbed|subs?|nordic|vf[fq0-9]?|hc|` +
		`amzn|nf|dsnp|atvp|hmax|itunes|` +
		`webrip|web-dl|webdl|bluray|blu-ray|bdrip|brrip|bdmv|hdtv|dvdrip|dvd|remux|web|` +
		`cam|hdcam|camrip|telesync|telecine|hdts|ts|tc|xvid|divx|` +
		`x264|x265|hevc|avc|h[ .]?26[45]|av1|` +
		`2160p|1080p|720p|576p|480p|4k|uhd|` +
		`epub|azw3|mobi` +
		`)\b`)

	reSeasonEpStrip = regexp.MustCompile(`(?i)\bs\d{1,2}(\s*-\s*s?\d{1,2})?(\s*e\d{1,3})?\b`)
	reSeasonWStrip  = regexp.MustCompile(`(?i)\bseasons?[ ._]*\d{1,2}\b`)
	reCompleteStrip = regexp.MustCompile(`(?i)\b(complete|collection|batch|all[ ._]seasons)\b`)
	reGroupSuffix   = regexp.MustCompile(`(?i)-[a-z0-9]+$`)

	reYearTok = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	// reGroupTag captures the trailing -GROUP of a scene release name.
	reGroupTag = regexp.MustCompile(`-([A-Za-z0-9]{2,15})\s*$`)
)

// Normalize reduces a release name to a bare content title for grouping:
// "[SubsPlease] Sousou no Frieren - 28 (1080p) [ABCD1234].mkv"
//
//	-> "sousou no frieren 28"
func Normalize(title string) string {
	s := title
	s = reExtension.ReplaceAllString(s, "")
	s = reParenYear.ReplaceAllString(s, " $1 ")
	s = reSepChars.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = reGroupSuffix.ReplaceAllString(s, "")
	return cleanTitle(s)
}

// cleanTitle runs the shared tail of the normalize pipeline: strip bracketed
// runs, season markers, and decoration tokens, then flatten to lowercase
// alphanumerics.
func cleanTitle(s string) string {
	s = reBracketed.ReplaceAllString(s, " ")
	s = reSeasonEpStrip.ReplaceAllString(s, " ")
	s = reSeasonWStrip.ReplaceAllString(s, " ")
	s = reCompleteStrip.ReplaceAllString(s, " ")
	s = reNoise.ReplaceAllString(s, " ")

	s = strings.ToLower(s)
	s = reNonAlnum.ReplaceAllString(s, " ")
	s = reMultiWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// SplitTitle cuts a release name into its content title and release year.
// Scene names lead with the title - "Title.Year.Res.Source.Audio-GROUP" - so
// the content is everything before the year (or before the first tech token
// when no year is present). The year may also hide inside bracketed metadata
// ("Title [2026, WEB-DL 1080p]"). A leading year is the title itself, not the
// release year ("1917 2019 1080p" -> "1917", 2019): among year tokens seen
// before the first tech token, the last one wins.
func SplitTitle(title string) (content, year string) {
	s := reExtension.ReplaceAllString(title, "")
	s = reParenYear.ReplaceAllString(s, " $1 ")
	s = reSepChars.ReplaceAllString(s, " ")

	techCut := len(s)
	for _, re := range []*regexp.Regexp{reNoise, reSeasonEpStrip, reSeasonWStrip, reCompleteStrip} {
		if loc := re.FindStringIndex(s); loc != nil && loc[0] < techCut {
			techCut = loc[0]
		}
	}
	cut := techCut
	for _, loc := range reYearTok.FindAllStringIndex(s, -1) {
		switch {
		case loc[0] == 0:
			// a year the title starts with is content, never the release year
		case loc[0] <= techCut:
			year = s[loc[0]:loc[1]]
			cut = loc[0]
		case year == "":
			year = s[loc[0]:loc[1]] // year buried past the tech tokens (bracketed metadata)
		}
	}
	content = cleanTitle(s[:cut])
	if content == "" {
		content = cleanTitle(s) // tech-looking token at position 0; fall back to the full clean
	}
	return content, year
}

// ReleaseGroup extracts the trailing -GROUP tag from a scene release name, or
// "" when the name does not look like a scene release (so a hyphenated plain
// title never loses its last word).
func ReleaseGroup(title string) string {
	s := reExtension.ReplaceAllString(strings.TrimSpace(title), "")
	if !reNoise.MatchString(s) {
		return ""
	}
	m := reGroupTag.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	g := m[1]
	if reNoise.MatchString(g) || reYearTok.MatchString(g) {
		return "" // "-AAC" / "-2026" is tech decoration, not a group
	}
	return g
}

// GroupKey clusters results that are the same content at the same quality:
// content title + year + resolution + season/episode marker.
func GroupKey(title string, t Tags) string {
	content, year := SplitTitle(title)
	return content + "|" + year + "|" + t.Resolution.String() + "|" + seasonMarker(t)
}

func seasonMarker(t Tags) string {
	switch {
	case t.SeasonEnd > 0:
		return fmt.Sprintf("s%02d-s%02d", t.Season, t.SeasonEnd)
	case t.Season > 0 && t.Episode > 0:
		return fmt.Sprintf("s%02de%02d", t.Season, t.Episode)
	case t.Season > 0:
		return fmt.Sprintf("s%02d", t.Season)
	}
	return ""
}

// ContentKey groups by title + year + season only (resolution-agnostic), used
// by autopilot to choose the best quality of the same content.
func ContentKey(title string, t Tags) string {
	content, year := SplitTitle(title)
	return content + "|" + year + "|" + seasonMarker(t)
}

// GroupLabel is a human-readable heading for a group: content title with the
// year, season, and resolution appended.
func GroupLabel(title string, t Tags) string {
	label, year := SplitTitle(title)
	if year != "" {
		label += " " + year
	}
	if m := seasonMarker(t); m != "" {
		label += " " + m
	}
	if res := t.Resolution.String(); res != "" {
		label += " · " + res
	}
	return label
}
