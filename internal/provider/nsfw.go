package provider

import "strings"

// Adult-content filtering, tuned to never cost a legitimate result. Two signals
// feed it: a provider's own category label (a strong, unambiguous signal where
// we have it, e.g. Knaben's "XXX") and a short list of brand/term tokens that
// essentially never appear in real movie/TV/software/anime titles.
//
// Deliberately absent from the token list: bare "xxx" and "sex". They collide
// with legitimate titles ("xXx (2002)", "Sex Education") and the category
// signal already covers the actual porn category, so leaving them out keeps the
// filter from ever eating a real release.

// adultCategories are lowercased category labels that mark a whole listing as
// adult. Providers set Result.Category from their own taxonomy.
var adultCategories = map[string]bool{
	"xxx":     true,
	"porn":    true,
	"adult":   true,
	"hentai":  true,
	"r18":     true,
	"jav":     true,
	"3d porn": true,
}

// adultTokens are unambiguous, word-boundary tokens. A title containing any of
// these (case-insensitively) is treated as adult regardless of category. Keep
// this list to brands and terms that do not occur in legitimate titles.
var adultTokens = []string{
	"onlyfans", "brazzers", "naughtyamerica", "realitykings", "bangbros",
	"blacked", "tushy", "vixen", "evilangel", "digitalplayground",
	"pornhub", "xvideos", "xnxx", "porn", "camwhores", "chaturbate",
	"myfreecams", "manyvids", "fansly", "javhd",
}

// isAdultResult reports whether a result is adult by category or by an
// unambiguous title token.
func isAdultResult(r Result) bool {
	if cat := strings.ToLower(strings.TrimSpace(r.Category)); cat != "" && adultCategories[cat] {
		return true
	}
	return containsAnyToken(r.Title, adultTokens)
}

// queryAllowsAdult reports whether the query itself unambiguously asks for adult
// content, in which case filtering is bypassed so a deliberate search is never
// crippled. Only the unambiguous brand/term tokens (onlyfans, brazzers, porn, …)
// count. Collision-prone words like "xxx" and "sex" are deliberately NOT a
// bypass: otherwise a legitimate "xXx 2002" or "Sex Education" search would turn
// the filter off entirely and leak XXX-category rows into the results.
func queryAllowsAdult(query string) bool {
	return containsAnyToken(query, adultTokens)
}

// containsAnyToken reports whether s contains any token as a whole word,
// case-insensitively. Word boundaries keep "porn" from matching "Portland".
func containsAnyToken(s string, tokens []string) bool {
	lower := strings.ToLower(s)
	for _, tok := range tokens {
		if hasWholeWord(lower, tok) {
			return true
		}
	}
	return false
}

// hasWholeWord reports whether lower (already lowercased) contains tok bounded
// by non-alphanumeric characters on both sides.
func hasWholeWord(lower, tok string) bool {
	from := 0
	for {
		i := strings.Index(lower[from:], tok)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(tok)
		if !isWordChar(lower, start-1) && !isWordChar(lower, end) {
			return true
		}
		from = start + 1
	}
}

func isWordChar(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// ContentFilter decides whether a result survives for a given query. The zero
// value (HideNSFW false) is a pass-through.
type ContentFilter struct {
	HideNSFW bool
}

// Allow reports whether r should be shown for query. When the query itself asks
// for adult content, nothing is hidden.
func (f ContentFilter) Allow(query string, r Result) bool {
	if !f.HideNSFW {
		return true
	}
	if queryAllowsAdult(query) {
		return true
	}
	return !isAdultResult(r)
}
