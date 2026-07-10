package rank

import (
	"regexp"
	"strings"
)

// NoiseReasons explains why a release is likely irrelevant to the search that
// produced it: dead swarm, cam-quality, off-topic upload, or a language-variant
// release the query never asked for. This is display-only; it never affects
// Score or ordering. query is the search terms, so a search for a language
// (e.g. "oppenheimer hindi") does not dim every release in that language.
func NoiseReasons(query, title string, t Tags, seeders int) []string {
	var reasons []string
	if seeders <= 0 {
		reasons = append(reasons, "zero seed")
	}
	if t.Source == SrcCam {
		reasons = append(reasons, "cam")
	}
	if reSoundtrack.MatchString(title) {
		reasons = append(reasons, "soundtrack")
	}
	if reRealStory.MatchString(title) {
		reasons = append(reasons, "real story")
	}
	if langVariant(query, title) {
		reasons = append(reasons, "language variant")
	}
	return reasons
}

// Noisy is the compact predicate kept for callers that only need dimming.
func Noisy(query, title string, t Tags, seeders int) bool {
	return len(NoiseReasons(query, title, t, seeders)) > 0
}

// langVariant reports a non-default-language release, but only when the search
// itself never mentioned any of the language tokens the title carries. If the
// user searched for one of them, they opted into that language and it is not
// noise - otherwise a plain "hindi" search would dim its own results.
func langVariant(query, title string) bool {
	toks := reLangVariant.FindAllString(title, -1)
	if len(toks) == 0 {
		return false
	}
	q := strings.ToLower(query)
	for _, tok := range toks {
		if strings.Contains(q, strings.ToLower(tok)) {
			return false
		}
	}
	return true
}

// These lists are small on purpose - expand only with clear user pain, since
// an over-eager match dims releases people actually want.
var (
	reSoundtrack  = regexp.MustCompile(`(?i)\b(soundtrack|ost|original score)\b`)
	reRealStory   = regexp.MustCompile(`(?i)\b(real story|true story|making of|behind the scenes)\b`)
	reLangVariant = regexp.MustCompile(`(?i)\b(dubbed|hindi|vostfr|latino|castellano|ita)\b`)
)
