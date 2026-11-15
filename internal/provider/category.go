package provider

import "strings"

func NormalizeCategory(raw string) string {
	raw = strings.ToLower(strings.Join(strings.Fields(raw), " "))
	raw = strings.ReplaceAll(raw, " - ", "/")
	segments := strings.FieldsFunc(raw, func(r rune) bool {
		return strings.ContainsRune(">/,|", r)
	})
	for _, segment := range segments {
		switch strings.TrimSpace(segment) {
		case "xxx", "nsfw", "adult", "porn":
			return ""
		}
	}
	category := ""
	for _, segment := range segments {
		candidate := ""
		switch strings.TrimSpace(segment) {
		case "movie", "movies", "film", "films":
			candidate = "movies"
		case "tv", "television", "tv shows", "tv show", "tv series":
			candidate = "tv"
		case "anime":
			candidate = "anime"
		case "music":
			candidate = "music"
		case "game", "games":
			candidate = "games"
		case "software", "app", "apps", "application", "applications":
			candidate = "software"
		case "book", "books", "ebook", "ebooks", "e-book", "e-books", "audiobook", "audiobooks":
			candidate = "books"
		case "linux", "linux iso", "linux isos":
			candidate = "linux"
		}
		if candidate == "" || candidate == category {
			continue
		}
		if category != "" {
			if (category == "tv" && candidate == "anime") || (category == "anime" && candidate == "tv") {
				category = "anime"
				continue
			}
			return ""
		}
		category = candidate
	}
	return category
}
