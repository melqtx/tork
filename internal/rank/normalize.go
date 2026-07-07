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
		`aac|ac3|eac3|dd5|dd\+|ddp|dts|dtshd|truehd|atmos|flac|opus|mp3|` +
		`hdr|hdr10|dolby|vision|dv|sdr|10bit|8bit|hi10p|` +
		`multi|dual|audio|repack|proper|internal|limited|extended|remastered|` +
		`unrated|uncut|imax|hybrid|readnfo|dubbed|subbed|` +
		`webrip|web-dl|webdl|bluray|blu-ray|bdrip|brrip|bdmv|hdtv|dvdrip|dvd|remux|web|` +
		`cam|hdcam|camrip|telesync|telecine|hdts|ts|tc|xvid|divx|` +
		`x264|x265|hevc|avc|h264|h265|av1|` +
		`2160p|1080p|720p|480p|4k|uhd` +
		`)\b`)

	reSeasonEpStrip = regexp.MustCompile(`(?i)\bs\d{1,2}(\s*-\s*s?\d{1,2})?(\s*e\d{1,3})?\b`)
	reSeasonWStrip  = regexp.MustCompile(`(?i)\bseasons?[ ._]*\d{1,2}\b`)
	reCompleteStrip = regexp.MustCompile(`(?i)\b(complete|collection|batch|all[ ._]seasons)\b`)
	reGroupSuffix   = regexp.MustCompile(`(?i)-[a-z0-9]+$`)
)

// Normalize reduces a release name to a bare content title for grouping:
// "[SubsPlease] Sousou no Frieren - 28 (1080p) [ABCD1234].mkv"
//
//	-> "sousou no frieren 28"
func Normalize(title string) string {
	s := title
	s = reExtension.ReplaceAllString(s, "")
	s = reParenYear.ReplaceAllString(s, " $1 ")
	s = reBracketed.ReplaceAllString(s, " ")
	s = reSepChars.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	s = reGroupSuffix.ReplaceAllString(s, "")

	s = reSeasonEpStrip.ReplaceAllString(s, " ")
	s = reSeasonWStrip.ReplaceAllString(s, " ")
	s = reCompleteStrip.ReplaceAllString(s, " ")
	s = reNoise.ReplaceAllString(s, " ")

	s = strings.ToLower(s)
	s = reNonAlnum.ReplaceAllString(s, " ")
	s = reMultiWS.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// GroupKey clusters results that are the same content at the same quality:
// normalized title + resolution + season/episode marker.
func GroupKey(title string, t Tags) string {
	base := Normalize(title)
	return base + "|" + t.Resolution.String() + "|" + seasonMarker(t)
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

// ContentKey groups by title + season only (resolution-agnostic), used by
// autopilot to choose the best quality of the same content.
func ContentKey(title string, t Tags) string {
	return Normalize(title) + "|" + seasonMarker(t)
}

// GroupLabel is a human-readable heading for a group: title cased with the
// season and resolution appended.
func GroupLabel(title string, t Tags) string {
	label := Normalize(title)
	if m := seasonMarker(t); m != "" {
		label += " " + m
	}
	if res := t.Resolution.String(); res != "" {
		label += " · " + res
	}
	return label
}
