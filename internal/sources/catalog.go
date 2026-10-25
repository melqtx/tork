package sources

import (
	"github.com/melqtx/tork/internal/config"
	"sort"
)

// Available is the ready-to-use catalog: disabled direct integrations with
// maintained adapters. Category-limited legacy TPB entries stay in settings,
// while new users get one all-category Pirate Bay choice.
func Available(cfg *config.Config) []Source {
	var out []Source
	for _, s := range List(cfg) {
		if s.Config.Enabled || !config.IsBuiltinSource(s.ID) || s.ID == "tpb_movies" || s.ID == "tpb_tv" {
			continue
		}
		out = append(out, s)
	}
	priority := map[string]int{"tpb": 1, "therarbg": 2, "limetorrents": 3, "x1337": 4, "eztv": 5, "knaben": 6, "yts": 7, "nyaa": 8}
	sort.SliceStable(out, func(i, j int) bool { return priority[out[i].ID] < priority[out[j].ID] })
	return out
}

func Description(s Source) string {
	switch Type(s.ID, s.Config) {
	case "tpb":
		return "Movies, TV, games, software, music and more. All Pirate Bay categories."
	case "therarbg":
		return "Movies, TV, games, software, music and books. Direct search; no setup."
	case "limetorrents":
		return "Movies, TV, games, software, music and anime. Direct search; no setup."
	case "knaben":
		return "Broad search across multiple indexes. No account required."
	case "1337x_html":
		return "Movies, TV, games, software and music. May be blocked on some networks."
	case "eztv_html":
		return "TV releases via EZTV's API, with TVmaze show-title lookup."
	case "yts":
		return "Movies from YTS."
	case "nyaa":
		return "Anime releases, including translated and raw editions."
	case "tpb_movies":
		return "Pirate Bay movies only. Choose The Pirate Bay in Add sources for all categories."
	case "tpb_tv":
		return "Pirate Bay TV only. Choose The Pirate Bay in Add sources for all categories."
	case "torznab":
		return "Custom source connected through an indexer manager."
	case "rss":
		return "Custom RSS search feed."
	}
	return "Custom source."
}
