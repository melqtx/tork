package sources

import (
	"strings"
	"testing"
)

func TestCustomConfigNormalizesCopiedTorznabURL(t *testing.T) {
	id, p, err := CustomConfig("Linux Index", "torznab", "http://localhost:9117/api/v2.0/indexers/linux/results/torznab/api?apikey=a%2Bkey&t=caps&q=old&cat=2000", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "linux-index" || p.APIKey != "a+key" || strings.Contains(p.SearchURL, "apikey") || !strings.Contains(p.SearchURL, "cat=2000") {
		t.Fatal("copied Torznab URL was not normalized")
	}
	for _, c := range []struct{ name, typ, endpoint, key string }{
		{"1337x", "torznab", "https://example.org/api", ""},
		{"Nyaa", "torznab", "https://example.org/api", ""},
		{"foo", "html", "https://example.org/api", ""},
		{"foo", "rss", "https://example.org/api", ""},
		{"foo", "rss", "https://example.org/api?q={query}", "secret"},
		{"foo", "torznab", "file:///tmp/secret", ""},
	} {
		_, _, err := CustomConfig(c.name, c.typ, c.endpoint, c.key)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid custom config: %v", err)
		}
	}
}
