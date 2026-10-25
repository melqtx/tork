package sources

import (
	"errors"
	"net/url"
	"strings"
	"unicode"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/provider"
)

// CustomConfig normalizes the form into an explicit supported feed config.
// Torznab URLs copied from an indexer manager work without hand-built templates.
func CustomConfig(name, typ, endpoint, apiKey string) (string, config.ProviderConfig, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 || name != provider.SanitizeDisplayText(name, 80) {
		return "", config.ProviderConfig{}, errors.New("enter a name of 1–80 plain-text characters")
	}
	var id strings.Builder
	for _, ch := range strings.ToLower(name) {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			id.WriteRune(ch)
		case unicode.IsSpace(ch), ch == '-', ch == '_':
			id.WriteByte('-')
		}
	}
	key := strings.Trim(id.String(), "-")
	if key == "" || len(key) > 64 {
		return "", config.ProviderConfig{}, errors.New("name must contain letters or numbers and fit in 64 ID characters")
	}
	if config.IsBuiltinSource(key) {
		return "", config.ProviderConfig{}, errors.New("this name belongs to a built-in source")
	}
	p := config.ProviderConfig{Label: name, Type: typ, SearchURL: strings.TrimSpace(endpoint), APIKey: strings.TrimSpace(apiKey)}
	switch typ {
	case "torznab":
		u, err := url.Parse(strings.ReplaceAll(p.SearchURL, "{query}", ""))
		if err != nil {
			return "", p, errors.New("enter an absolute HTTP(S) endpoint URL")
		}
		q := u.Query()
		if p.APIKey == "" {
			p.APIKey = q.Get("apikey")
		}
		q.Del("apikey")
		q.Del("q")
		q.Del("t")
		q.Del("extended")
		u.RawQuery = q.Encode()
		p.SearchURL = u.String()
	case "rss":
		if p.APIKey != "" {
			return "", p, errors.New("separate API keys are supported for Torznab; put RSS authentication in its search URL")
		}
		if !strings.Contains(p.SearchURL, "{query}") {
			return "", p, errors.New("RSS search URLs must contain {query} where the search text belongs")
		}
	default:
		return "", p, errors.New("choose RSS or Torznab")
	}
	if _, err := Build(key, p, nil); err != nil {
		return "", p, err
	}
	return key, p, nil
}
