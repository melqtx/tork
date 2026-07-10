// Package rank turns raw torrent release names into structured tags,
// normalized grouping keys, and quality scores. It is the shared foundation
// for smart ranking, the source-graph view, and autopilot.
package rank

import (
	"regexp"
	"strconv"
)

type Resolution int

const (
	ResUnknown Resolution = iota
	Res480
	Res576
	Res720
	Res1080
	Res2160
)

func (r Resolution) String() string {
	switch r {
	case Res480:
		return "480p"
	case Res576:
		return "576p"
	case Res720:
		return "720p"
	case Res1080:
		return "1080p"
	case Res2160:
		return "2160p"
	}
	return ""
}

type Source int

const (
	SrcUnknown Source = iota
	SrcCam
	SrcDVD
	SrcHDTV
	SrcWebRip
	SrcWebDL
	SrcBluRay
	SrcRemux
)

func (s Source) String() string {
	switch s {
	case SrcCam:
		return "CAM"
	case SrcDVD:
		return "DVD"
	case SrcHDTV:
		return "HDTV"
	case SrcWebRip:
		return "WEBRip"
	case SrcWebDL:
		return "WEB-DL"
	case SrcBluRay:
		return "BluRay"
	case SrcRemux:
		return "REMUX"
	}
	return ""
}

// Tags is the structured interpretation of a release name. All fields are
// best-effort; a zero value simply means "not detected".
type Tags struct {
	Resolution Resolution
	Source     Source
	Codec      string // "x265", "x264", "av1", ""
	HDR        bool   // HDR / HDR10 / HDR10+
	DV         bool   // Dolby Vision
	Season     int    // 0 = none
	Episode    int    // 0 = none
	SeasonEnd  int    // >0 only for ranges like S01-S05
	Complete   bool   // complete/collection/batch, or a season range
}

// Compile once; matching is case-insensitive over the raw title.
var (
	reSeasonRange = regexp.MustCompile(`(?i)\bs(\d{1,2})\s*-\s*s?(\d{1,2})\b`)
	reSeasonEp    = regexp.MustCompile(`(?i)\bs(\d{1,2})(?:\s*e(\d{1,3}))?\b`)
	reSeasonWord  = regexp.MustCompile(`(?i)\bseasons?[ ._]*(\d{1,2})\b`)

	reRes2160 = regexp.MustCompile(`(?i)\b(2160p|4k|uhd)\b`)
	reRes1080 = regexp.MustCompile(`(?i)\b1080p\b`)
	reRes720  = regexp.MustCompile(`(?i)\b720p\b`)
	reRes576  = regexp.MustCompile(`(?i)\b576p\b`)
	reRes480  = regexp.MustCompile(`(?i)\b480p\b`)

	reHDR = regexp.MustCompile(`(?i)\bhdr(10)?(p|plus|\+)?\b`)
	reDV  = regexp.MustCompile(`(?i)\b(dv|dovi|dolby[ ._]?vision)\b`)

	reRemux  = regexp.MustCompile(`(?i)\bremux\b`)
	reBluRay = regexp.MustCompile(`(?i)\b(blu-?ray|bdrip|brrip|bdmv)\b`)
	reWebRip = regexp.MustCompile(`(?i)\bweb-?rip\b`)
	reWebDL  = regexp.MustCompile(`(?i)\bweb[ ._-]?dl\b|\bweb\b`) // bare WEB defaults to WEB-DL
	reHDTV   = regexp.MustCompile(`(?i)\bhdtv\b`)
	reDVD    = regexp.MustCompile(`(?i)\b(dvdrip|dvd)\b`)
	reCam    = regexp.MustCompile(`(?i)\b(cam|hdcam|camrip|telesync|telecine|hdts|ts|tc)\b`)

	reX265 = regexp.MustCompile(`(?i)\b(x265|hevc|h[ .]?265)\b`)
	reX264 = regexp.MustCompile(`(?i)\b(x264|h[ .]?264|avc)\b`)
	reAV1  = regexp.MustCompile(`(?i)\bav1\b`)

	reComplete = regexp.MustCompile(`(?i)\b(complete|collection|batch|all[ ._]seasons)\b`)
)

// Parse extracts structured tags from a release name.
func Parse(title string) Tags {
	var t Tags

	switch {
	case reRes2160.MatchString(title):
		t.Resolution = Res2160
	case reRes1080.MatchString(title):
		t.Resolution = Res1080
	case reRes720.MatchString(title):
		t.Resolution = Res720
	case reRes576.MatchString(title):
		t.Resolution = Res576
	case reRes480.MatchString(title):
		t.Resolution = Res480
	}

	t.HDR = reHDR.MatchString(title)
	t.DV = reDV.MatchString(title)

	// Source, strongest signal first.
	switch {
	case reRemux.MatchString(title):
		t.Source = SrcRemux
	case reBluRay.MatchString(title):
		t.Source = SrcBluRay
	case reWebRip.MatchString(title):
		t.Source = SrcWebRip
	case reWebDL.MatchString(title):
		t.Source = SrcWebDL
	case reHDTV.MatchString(title):
		t.Source = SrcHDTV
	case reDVD.MatchString(title):
		t.Source = SrcDVD
	case reCam.MatchString(title):
		t.Source = SrcCam
	}

	switch {
	case reAV1.MatchString(title):
		t.Codec = "av1"
	case reX265.MatchString(title):
		t.Codec = "x265"
	case reX264.MatchString(title):
		t.Codec = "x264"
	}

	// Season / episode. Ranges win over single-season matches.
	if m := reSeasonRange.FindStringSubmatch(title); m != nil {
		t.Season = atoi(m[1])
		t.SeasonEnd = atoi(m[2])
		t.Complete = true
	} else if m := reSeasonEp.FindStringSubmatch(title); m != nil {
		t.Season = atoi(m[1])
		if m[2] != "" {
			t.Episode = atoi(m[2])
		}
	} else if m := reSeasonWord.FindStringSubmatch(title); m != nil {
		t.Season = atoi(m[1])
	}

	if reComplete.MatchString(title) {
		t.Complete = true
	}

	return t
}

// IsPack reports whether the release covers a whole season (or more) rather
// than a single episode: a season with no episode, a range, or a complete set.
func (t Tags) IsPack() bool {
	if t.Complete || t.SeasonEnd > 0 {
		return true
	}
	return t.Season > 0 && t.Episode == 0
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
