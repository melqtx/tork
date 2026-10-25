package tui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Keep spelling, year and episode markers; only trim recognized release
// metadata. The untouched provider title remains in the detail panel.
var releaseTech = regexp.MustCompile(`(?i)\b(?:2160p|1080[pi]|720p|576p|480p|web[ .-]?dl|webrip|blu[ .-]?ray|bdrip|brrip|hdtv|remux|x26[45]|h[ .]?26[45]|hevc|av1)\b`)
var releaseExtension = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|torrent)$`)

func readableReleaseTitle(raw string) string {
	runes := []rune(releaseExtension.ReplaceAllString(raw, ""))
	for i, c := range runes {
		if c == '_' || c == '.' && !(i > 0 && i+1 < len(runes) && unicode.IsDigit(runes[i-1]) && unicode.IsDigit(runes[i+1])) {
			runes[i] = ' '
		}
	}
	title := string(runes)
	if loc := releaseTech.FindStringIndex(title); loc != nil && strings.TrimSpace(title[:loc[0]]) != "" {
		title = title[:loc[0]]
	}
	title = strings.TrimRight(strings.TrimSpace(title), " .-([")
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return raw
	}
	return title
}
func releaseQuality(row scoredRow) string {
	parts := []string{}
	if s := row.tags.Source.String(); s != "" {
		parts = append(parts, s)
	}
	if row.tags.Codec != "" {
		parts = append(parts, row.tags.Codec)
	}
	if row.tags.DV {
		parts = append(parts, "DV")
	} else if row.tags.HDR {
		parts = append(parts, "HDR")
	}
	if len(parts) == 0 {
		if row.res.Category != "" {
			return row.res.Category
		}
		return sourceTag(row.res, true)
	}
	return strings.Join(parts, " · ")
}
func renderReleaseRow(row scoredRow, width int) string {
	l := newReleaseLayout(width)
	size := "size ?"
	if row.res.SizeBytes > 0 {
		size = humanBytes(row.res.SizeBytes)
	}
	availability := fmt.Sprintf("%d seeds", max(0, row.res.Seeders))
	if row.res.Seeders <= 0 {
		availability = "no seeds"
	}
	quality := releaseQuality(row)
	if quality == "source unknown" {
		quality = "—"
	}
	return l.render(readableReleaseTitle(row.res.Title), quality, size, availability, false)
}

// The header and every release use the same grid. Drop optional columns on
// small terminals instead of letting a large swarm count move the title.
type releaseLayout struct{ title, quality, size, seeds int }

func newReleaseLayout(width int) releaseLayout {
	l := releaseLayout{title: width}
	if width >= 20 {
		l.size = 9
		l.title -= l.size + 2
	}
	if width >= 44 {
		l.seeds = 12
		l.title -= l.seeds + 2
	}
	if width >= 76 {
		l.quality = 24
		l.title -= l.quality + 2
	}
	return l
}

func (l releaseLayout) render(title, quality, size, seeds string, header bool) string {
	parts := []string{padRight(truncate(title, l.title), l.title)}
	if l.quality > 0 {
		parts = append(parts, padRight(truncate(quality, l.quality), l.quality))
	}
	if l.size > 0 {
		parts = append(parts, padLeft(truncate(size, l.size), l.size))
	}
	if l.seeds > 0 {
		parts = append(parts, padLeft(truncate(seeds, l.seeds), l.seeds))
	}
	if header {
		return styleFaint.Render(strings.Join(parts, "  "))
	}
	// Keep titles bright while the supporting columns recede.
	line := styleFg.Render(parts[0])
	if len(parts) > 1 {
		line += "  " + styleDim.Render(strings.Join(parts[1:], "  "))
	}
	return line
}

func releaseHeader(width int) string {
	return newReleaseLayout(width).render("Release", "Details", "Size", "Availability", true)
}
