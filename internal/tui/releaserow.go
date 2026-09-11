package tui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
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
		return "source unknown"
	}
	return strings.Join(parts, " · ")
}
func renderReleaseRow(row scoredRow, width int) string {
	title := readableReleaseTitle(row.res.Title)
	size := "size ?"
	if row.res.SizeBytes > 0 {
		size = humanBytes(row.res.SizeBytes)
	}
	availability := fmt.Sprintf("%d seeds", max(0, row.res.Seeders))
	if row.res.Seeders <= 0 {
		availability = "no seeds"
	}
	right := size + " · " + availability
	quality := ""
	if width >= 76 {
		quality = padRight(truncate(releaseQuality(row), 25), 25) + "  "
	}
	if width < 44 {
		right = size
	}
	titleWidth := max(1, width-lipgloss.Width(quality)-lipgloss.Width(right)-2)
	return truncate(padRight(truncate(title, titleWidth), titleWidth)+"  "+styleDim.Render(quality+right), width)
}
