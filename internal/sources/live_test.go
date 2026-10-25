package sources

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/provider"
)

// Opt-in smoke check: search metadata only, no engine, downloads, user config
// edits, or assertions about third-party availability.
func TestLiveBuiltinSources(t *testing.T) {
	if os.Getenv("TORK_LIVE_SOURCES") != "1" {
		t.Skip("set TORK_LIVE_SOURCES=1 for live source probes")
	}
	cfg := config.Default(t.TempDir())
	var all []provider.Provider
	for _, s := range List(cfg) {
		p, err := Build(s.ID, s.Config, cfg.ProxyHTTPClient())
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, p)
	}
	for _, p := range health.ProbeProviders(context.Background(), all, 15*time.Second) {
		state := "responded"
		if !p.OK {
			state = p.Err
		}
		t.Logf("%s: %s; usable_results=%d; elapsed=%dms", p.Name, state, p.Results, p.LatencyMS)
	}
}

// This stricter release check verifies both live search and a usable selected
// result for the new broad sources. It fails on unavailable/broken adapters.
func TestLiveBroadSources(t *testing.T) {
	if os.Getenv("TORK_LIVE_SOURCES") != "1" {
		t.Skip("set TORK_LIVE_SOURCES=1 for live search and magnet checks")
	}
	cfg := config.Default(t.TempDir())
	for _, id := range []string{"therarbg", "limetorrents", "tpb", "eztv", "knaben"} {
		t.Run(id, func(t *testing.T) {
			p, err := Build(id, cfg.Providers[id], cfg.ProxyHTTPClient())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			out := make(chan provider.Result, 200)
			query := "ubuntu"
			if id == "tpb" {
				query = "linux"
			}
			if id == "eztv" {
				query = "severance s02e01"
			}
			if err := p.Search(ctx, query, out); err != nil {
				t.Fatalf("live search failed: %v", err)
			}
			close(out)
			var results []provider.Result
			for r := range out {
				results = append(results, r)
			}
			if len(results) == 0 {
				t.Fatalf("live %q search returned no results", query)
			}
			first := results[0]
			magnet := first.Magnet
			if magnet == "" {
				resolver, ok := p.(provider.MagnetResolver)
				if !ok {
					t.Fatal("result has neither a magnet nor a resolver")
				}
				magnet, err = resolver.ResolveMagnet(ctx, first)
				if err != nil {
					t.Fatalf("selected result failed to resolve: %v", err)
				}
			}
			if (provider.Result{Magnet: magnet}).InfoHash() == "" {
				t.Fatal("selected result has an invalid magnet")
			}
			t.Logf("search_results=%d; selected_result_magnet=valid; category=%s", len(results), first.Category)
			if id == "knaben" {
				empty := make(chan provider.Result, 200)
				if err := p.Search(ctx, "torknomatchd4b619580f12", empty); err != nil {
					t.Fatalf("real empty search failed validation: %v", err)
				}
				if len(empty) != 0 {
					t.Fatal("empty query returned unrelated results")
				}
				t.Log("recognizable empty search=valid")
			}
		})
	}
}
