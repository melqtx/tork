// Package sources builds configured search providers for the CLI and TUI.
package sources

import (
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/provider"
)

type Source struct {
	ID          string
	Label       string
	RuntimeName string
	Config      config.ProviderConfig
}

// List is deterministic and includes disabled sources. IDs refer to config
// entries, not adapter names (x1337 versus 1337x, for example).
func List(cfg *config.Config) []Source {
	if cfg == nil {
		return nil
	}
	var out []Source
	seen := make(map[string]bool)
	for _, builtin := range config.BuiltinSources() {
		if p, ok := cfg.Providers[builtin.ID]; ok {
			out = append(out, Source{builtin.ID, builtin.Label, builtin.RuntimeName, p})
			seen[builtin.ID] = true
		}
	}
	var custom []string
	for id := range cfg.Providers {
		if !seen[id] {
			custom = append(custom, id)
		}
	}
	sort.Strings(custom)
	for _, id := range custom {
		out = append(out, Source{id, sourceLabel(id, cfg.Providers[id]), id, cfg.Providers[id]})
	}
	return out
}

func Enabled(cfg *config.Config) []provider.Provider {
	var out []provider.Provider
	client := cfg.ProxyHTTPClient()
	for _, s := range List(cfg) {
		if !s.Config.Enabled {
			continue
		}
		// Preserve the previous startup behavior: an unsupported custom type does
		// not stop other sources. Sources exposes the configuration error explicitly.
		if p, err := Build(s.ID, s.Config, client); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func Type(id string, p config.ProviderConfig) string {
	if p.Type != "" {
		return p.Type
	}
	for _, s := range config.BuiltinSources() {
		if s.ID == id {
			return s.Config.Type
		}
	}
	return id
}

// Build also validates disabled sources for an explicit check or enable action.
// Errors contain no configured URLs, query parameters or credentials.
func Build(id string, p config.ProviderConfig, client *http.Client) (provider.Provider, error) {
	kind := Type(id, p)
	if kind != "rss" && kind != "torznab" {
		for _, raw := range p.BaseURLs() {
			if !validHTTPURL(raw) {
				return nil, errors.New("mirror must be an absolute HTTP(S) URL")
			}
		}
	}
	switch kind {
	case "knaben":
		return provider.NewKnaben(client, p.Mirror), nil
	case "yts":
		return provider.NewYTS(client, p.Mirror), nil
	case "nyaa":
		return provider.NewNyaa(client, p.Mirror), nil
	case "eztv_html":
		return provider.NewEZTV(client, p.Mirror), nil
	case "1337x_html":
		return provider.NewX1337(client, p.BaseURLs()), nil
	case "tpb":
		return provider.NewPirateBay(client, p.BaseURLs()...), nil
	case "therarbg":
		return provider.NewRARBG(client, p.BaseURLs()...), nil
	case "limetorrents":
		return provider.NewLimeTorrents(client, p.BaseURLs()...), nil
	case "tpb_movies":
		return provider.NewPirateBayMovies(client, p.BaseURLs()...), nil
	case "tpb_tv":
		return provider.NewPirateBayTV(client, p.BaseURLs()...), nil
	case "rss", "torznab":
		raw := strings.ReplaceAll(strings.ReplaceAll(p.SearchURL, "{query}", "test"), "%s", "test")
		if !validHTTPURL(raw) {
			return nil, errors.New("search_url must be an absolute HTTP(S) URL")
		}
		if kind == "torznab" {
			return provider.NewTorznab(client, id, p.SearchURL, p.APIKey), nil
		}
		if p.APIKey != "" {
			return nil, errors.New("RSS authentication belongs in its search URL; separate API keys require Torznab")
		}
		return provider.NewRSS(client, id, p.SearchURL), nil
	}
	return nil, errors.New("unsupported source type")
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.Fragment == ""
}

func sourceLabel(id string, p config.ProviderConfig) string {
	if p.Label != "" {
		return provider.SanitizeDisplayText(p.Label, 80)
	}
	return provider.SanitizeDisplayText(id, 80)
}
