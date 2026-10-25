package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/provider"
)

func TestConfiguredBuiltinAdaptersSearchAndResolve(t *testing.T) {
	cases := []struct {
		id, fixture, query, name string
		count                    int
	}{
		{"knaben", "knaben.html", "fight club", "knaben", 4},
		{"yts", "yts.json", "inception", "yts", 2},
		{"nyaa", "nyaa.rss", "frieren", "nyaa", 2},
		{"tpb_movies", "apibay.json", "", "tpb-movies", 2},
		{"tpb_tv", "apibay.json", "", "tpb-tv", 2},
		{"eztv", "eztv.html", "", "eztv", 3},
		{"x1337", "1337x_search.html", "ubuntu", "1337x", 2},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "provider", "testdata", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			detail, err := os.ReadFile(filepath.Join("..", "provider", "testdata", "1337x_detail.html"))
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/torrent/") {
					w.Write(detail)
				} else {
					w.Write(data)
				}
			}))
			defer srv.Close()
			// Exercise the same factory and saved IDs used by Sources. Omit type so
			// legacy configs also exercise builtin type inference.
			p, err := Build(tc.id, config.ProviderConfig{Mirror: srv.URL}, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if p.Name() != tc.name {
				t.Fatalf("name %q, want %q", p.Name(), tc.name)
			}
			out := make(chan provider.Result, 100)
			if err := p.Search(context.Background(), tc.query, out); err != nil {
				t.Fatal(err)
			}
			close(out)
			var results []provider.Result
			for r := range out {
				results = append(results, r)
			}
			if len(results) != tc.count {
				t.Fatalf("got %d results, want %d", len(results), tc.count)
			}
			for _, r := range results {
				magnet := r.Magnet
				if resolver, ok := p.(provider.MagnetResolver); ok && magnet == "" {
					magnet, err = resolver.ResolveMagnet(context.Background(), r)
					if err != nil {
						t.Fatal(err)
					}
				}
				if !strings.HasPrefix(magnet, "magnet:?") {
					t.Fatalf("unusable result: %+v", r)
				}
			}
		})
	}
}

func TestCatalogRetainsDefaultsAndCustomFeedOrder(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Providers["z_feed"] = config.ProviderConfig{Enabled: true, Type: "torznab", SearchURL: "https://example.org/?q={query}"}
	cfg.Providers["a_feed"] = config.ProviderConfig{Type: "rss", SearchURL: "https://example.org/?q=%s"}
	list := List(cfg)
	if len(list) != len(config.BuiltinSources())+2 || list[6].ID != "x1337" || list[len(list)-2].ID != "a_feed" || list[len(list)-1].ID != "z_feed" {
		t.Fatalf("catalog order: %+v", list)
	}
	enabled := Enabled(cfg)
	var names []string
	for _, p := range enabled {
		names = append(names, p.Name())
	}
	if strings.Join(names, ",") != "knaben,yts,nyaa,z_feed" {
		t.Fatalf("enabled sources: %v", names)
	}
}

func TestInvalidSourceConfigDoesNotLeakCredentials(t *testing.T) {
	for _, cfg := range []config.ProviderConfig{
		{Type: "unknown-secret"},
		{Type: "rss", SearchURL: "file:///tmp/secret"},
		{Type: "torznab", SearchURL: "https://bad%ZZsecret/?apikey=secret"},
		{Type: "yts", Mirror: "https://bad%secret"},
	} {
		_, err := Build("test", cfg, nil)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe config error: %v", err)
		}
	}
}
