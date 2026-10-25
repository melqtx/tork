package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/sources"
)

// Drive batched commands exactly as Bubble Tea does, but keep automatic checks
// on local fixtures. This covers actual requests, not just queued flags.
func runSourceChecks(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, sub := range msg {
			runSourceChecks(t, a, sub)
		}
	case sourceCheckedMsg:
		a.Update(msg)
	}
}

func TestBlockedSourceCanRecoverWithOneExplicitCheck(t *testing.T) {
	var calls atomic.Int32
	var recovered atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if !recovered.Load() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`<rss><channel/></rss>`))
	}))
	defer srv.Close()
	a := sourcesTestApp(t)
	a.cfg.Providers = map[string]config.ProviderConfig{"local": {Enabled: true, Type: "rss", SearchURL: srv.URL}}
	a.agg = aggregator.New(sources.Enabled(a.cfg), time.Second, 0)
	runSourceChecks(t, a, a.navigate(screenSources))
	if calls.Load() != 1 || !strings.Contains(a.View(), "t retry now") || !strings.Contains(a.View(), "retry in 10m") {
		t.Fatal("blocked source lacks its recovery action and retry time")
	}
	if cmd := a.navigate(screenSources); cmd != nil {
		t.Fatal("navigation bypassed a blocked source's pause")
	}
	results, status := a.agg.Search(context.Background(), "example")
	for range results {
	}
	for range status {
	}
	if calls.Load() != 1 {
		t.Fatal("search ignored the source check's block")
	}
	recovered.Store(true)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	runSourceChecks(t, a, cmd)
	if calls.Load() != 2 || !a.agg.BlockedUntil("local").IsZero() || !a.cfg.Providers["local"].Enabled {
		t.Fatal("explicit retry failed to recover or changed the saved choice")
	}
	results, status = a.agg.Search(context.Background(), "example")
	for range results {
	}
	for ev := range status {
		if ev.State == aggregator.StateFailed {
			t.Fatal("search still paused after a successful explicit check")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("recovered source was not searched")
	}
}

func TestSearchSourcePanelRetriesSelectedSourceDirectly(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Write([]byte(`<rss><channel/></rss>`))
	}))
	defer srv.Close()
	a := sourcesTestApp(t)
	a.cfg.Providers = map[string]config.ProviderConfig{"local": {Enabled: true, Type: "rss", SearchURL: srv.URL}}
	a.agg = aggregator.New(sources.Enabled(a.cfg), time.Second, 0)
	a.agg.RecordBlocked("local")
	a.screen = screenResults
	a.results.sourcePanel = true
	a.results.query = "saved search"
	_, retry := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if a.screen != screenSources || a.results.query != "saved search" || a.sources.win.cursor != 0 {
		t.Fatal("retry lost the search or failed to open the selected source")
	}
	runSourceChecks(t, a, retry)
	if calls.Load() != 1 || !a.agg.BlockedUntil("local").IsZero() {
		t.Fatal("direct retry duplicated requests or left the source paused")
	}
}

func TestSourcesCheckAllEnabledOnEntryWithoutDuplicates(t *testing.T) {
	var first, second, disabled atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/one":
			first.Add(1)
		case "/two":
			second.Add(1)
		case "/off":
			disabled.Add(1)
		}
		w.Write([]byte(`<rss><channel/></rss>`))
	}))
	defer srv.Close()
	a := sourcesTestApp(t)
	a.cfg.Providers = map[string]config.ProviderConfig{
		"one": {Type: "rss", Enabled: true, SearchURL: srv.URL + "/one"},
		"two": {Type: "rss", Enabled: true, SearchURL: srv.URL + "/two"},
		"off": {Type: "rss", SearchURL: srv.URL + "/off"},
	}
	cmd := a.navigate(screenSources)
	if !a.sources.checking["one"] || !a.sources.checking["two"] || a.sources.checking["off"] {
		t.Fatal("opening sources did not schedule exactly the enabled sources")
	}
	if extra := a.checkEnabledSources(true); extra != nil {
		t.Fatal("duplicated in-flight checks")
	}
	runSourceChecks(t, a, cmd)
	if first.Load() != 1 || second.Load() != 1 || disabled.Load() != 0 {
		t.Fatal("checked wrong sources")
	}
	if strings.Count(a.View(), "working") < 2 {
		t.Fatal("completed enabled checks still missing working status")
	}
	if extra := a.navigate(screenSources); extra != nil {
		t.Fatal("reopening rechecked fresh sources")
	}
	r := a.sources.readings["two"]
	r.at = time.Now().Add(-sourceReadingTTL - time.Second)
	a.sources.readings["two"] = r
	runSourceChecks(t, a, a.navigate(screenSources))
	if first.Load() != 1 || second.Load() != 2 {
		t.Fatal("stale readings were not selectively refreshed")
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	runSourceChecks(t, a, cmd)
	if first.Load() != 2 || second.Load() != 3 || disabled.Load() != 0 {
		t.Fatal("manual recheck skipped enabled sources or probed disabled source")
	}
}

func TestEnablingAnExistingSourceAutomaticallyChecksIt(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.Write([]byte(`<rss><channel/></rss>`)) }))
	defer srv.Close()
	a := sourcesTestApp(t)
	// Use an existing built-in config slot so its enabled flag can be saved to
	// the on-disk config, with its transport pointed at the local fixture.
	p := a.cfg.Providers["eztv"]
	p.Type = "rss"
	p.SearchURL = srv.URL
	a.cfg.Providers["eztv"] = p
	a.screen = screenSources
	selectSource(a, "eztv")
	_, save := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_, check := a.Update(save())
	if !a.sources.checking["eztv"] {
		t.Fatal("enable left source untested")
	}
	runSourceChecks(t, a, check)
	if calls.Load() != 1 || !a.sources.readings["eztv"].probe.OK {
		t.Fatal("enabled source was not verified")
	}
}

func TestHistoricalSuccessCannotSkipCheckingCurrentEndpoint(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Write([]byte(`<html><title>Login</title></html>`))
	}))
	defer srv.Close()
	a := sourcesTestApp(t)
	a.cfg.Providers = map[string]config.ProviderConfig{"changed": {Enabled: true, Type: "rss", SearchURL: srv.URL}}
	a.health = health.Open(a.cfg.HealthPath())
	if err := a.health.Append(health.Snapshot{At: time.Now().UTC(), Kind: health.KindManual, Providers: []health.ProviderProbe{{Name: "changed", OK: true, Results: 100}}}); err != nil {
		t.Fatal(err)
	}
	cmd := a.navigate(screenSources)
	if !a.sources.checking["changed"] {
		t.Fatal("older healthy endpoint suppressed a current check")
	}
	runSourceChecks(t, a, cmd)
	if calls.Load() != 1 || strings.Contains(a.View(), "working ·") {
		t.Fatal("invalid current endpoint inherited an old working status")
	}
}
