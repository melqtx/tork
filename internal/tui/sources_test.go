package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/aggregator"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/health"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/sources"
	"github.com/melqtx/tork/internal/state"
)

func sourcesTestApp(t *testing.T) *App {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, nil, aggregator.New(sources.Enabled(cfg), time.Second, 0).WithFilter(provider.ContentFilter{HideNSFW: true}), &state.State{}, nil)
}

func selectSource(a *App, id string) {
	for i, s := range sources.List(a.cfg) {
		if s.ID == id {
			a.sources.win.cursor = i
			return
		}
	}
	panic("missing source")
}

func TestSourcesHomeNavigationSaveReloadAndFailure(t *testing.T) {
	a := sourcesTestApp(t)
	a.width, a.height = 100, 30
	a.search.input.SetValue("saved query")
	for _, d := range a.homeDestinations() {
		a.Update(tea.KeyMsg{Type: tea.KeyDown})
		if d.screen == screenSources {
			break
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.screen != screenSources {
		t.Fatal("home did not open sources")
	}
	if !strings.Contains(a.View(), "Pirate Bay (movies)") {
		t.Fatal("opt-in sources are not discoverable")
	}
	selectSource(a, "eztv")
	old := a.agg
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeySpace})
	if cmd == nil || a.cfg.Providers["eztv"].Enabled {
		t.Fatal("choice applied before it was saved")
	}
	// Ignore repeated toggles until this disk edit completes.
	if _, repeated := a.Update(tea.KeyMsg{Type: tea.KeyEnter}); repeated != nil {
		t.Fatal("concurrent save started")
	}
	a.Update(cmd())
	if a.agg != old || a.nextAgg == nil || !a.cfg.Providers["eztv"].Enabled {
		t.Fatal("save replaced active search or failed to prepare next one")
	}
	cfg, err := config.LoadReadOnlyFrom(filepath.Dir(a.cfg.ConfigPath()))
	if err != nil || !cfg.Providers["eztv"].Enabled {
		t.Fatalf("choice did not survive reload: %v", err)
	}
	// A real save failure must leave disk and in-memory choices unchanged.
	if err := os.Remove(a.cfg.ConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(a.cfg.ConfigPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	selectSource(a, "nyaa")
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeySpace})
	a.Update(cmd())
	if !a.cfg.Providers["nyaa"].Enabled || a.sources.saving != "" || !strings.Contains(a.errText, "not saved") {
		t.Fatal("failed save changed selection or stayed busy")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if a.screen != screenDownloads {
		t.Fatal("downloads shortcut failed")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if a.screen != screenSources {
		t.Fatal("downloads lost return screen")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.screen != screenSearch || !a.search.input.Focused() || a.search.input.Value() != "saved query" {
		t.Fatal("return home lost search focus or query")
	}
}

type heldResolver struct {
	release   chan struct{}
	cancelled chan struct{}
}

func (p *heldResolver) Name() string { return "1337x" }
func (p *heldResolver) Search(ctx context.Context, _ string, out chan<- provider.Result) error {
	for i := 0; i < 2; i++ {
		if i == 1 {
			select {
			case <-p.release:
			case <-ctx.Done():
				close(p.cancelled)
				return ctx.Err()
			}
		}
		select {
		case out <- provider.Result{Title: "Linux 1080p", Provider: p.Name(), DetailURL: "https://example.org/detail"}:
		case <-ctx.Done():
			close(p.cancelled)
			return ctx.Err()
		}
	}
	return nil
}
func (p *heldResolver) ResolveMagnet(context.Context, provider.Result) (string, error) {
	return provider.BuildMagnet(rowHash, "Linux", nil), nil
}

func TestSourceChangesPreserveLiveSearchAndLazyResolver(t *testing.T) {
	a := sourcesTestApp(t)
	// The next search is an offline feed, so accidentally using a disabled
	// builtin is observable without contacting an external site.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<rss><channel/></rss>")) }))
	defer srv.Close()
	for id, p := range a.cfg.Providers {
		p.Enabled = false
		a.cfg.Providers[id] = p
	}
	p := a.cfg.Providers["x1337"]
	p.Enabled = true
	a.cfg.Providers["x1337"] = p
	a.cfg.Providers["local"] = config.ProviderConfig{Enabled: true, Type: "rss", SearchURL: srv.URL}
	held := &heldResolver{release: make(chan struct{}), cancelled: make(chan struct{})}
	a.agg = aggregator.New([]provider.Provider{held}, time.Second, 0)
	old := a.agg
	a.startSearch("linux")
	a.navigate(screenSources)
	selectSource(a, "x1337")
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeySpace})
	a.Update(cmd())
	if a.agg != old || a.findResolver("1337x") != held {
		t.Fatal("source change lost active search resolver")
	}
	select {
	case <-held.cancelled:
		t.Fatal("toggle cancelled current search")
	default:
	}
	// Both the already queued result and a later result still reach the old
	// search while Sources is visible.
	close(held.release)
	for r := range a.results.resultCh {
		a.Update(resultMsg{searchID: a.results.searchID, r: r})
	}
	for range a.results.statusCh {
	}
	if len(a.results.rows) != 1 || a.screen != screenSources {
		t.Fatal("background results failed to arrive")
	}
	magnet, err := a.findResolver("1337x").ResolveMagnet(context.Background(), a.results.rows[0].res)
	if err != nil || magnet == "" {
		t.Fatal("old result is no longer selectable")
	}
	a.startSearch("next")
	if a.agg == old || a.nextAgg != nil || len(a.agg.Providers()) != 1 || a.agg.Providers()[0].Name() != "local" {
		t.Fatal("next search ignored new source selection")
	}
	for range a.results.resultCh {
	}
	for range a.results.statusCh {
	}
}

func TestDisabledSourceChecksAreExplicitBoundedAndDoNotEnable(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusForbidden)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "1080p" {
			t.Error("check did not send the documented canary")
		}
		code := int(status.Load())
		w.WriteHeader(code)
		if code == http.StatusOK {
			w.Write([]byte("<rss><channel/></rss>"))
		}
	}))
	defer srv.Close()
	a := sourcesTestApp(t)
	a.cfg.Providers["local"] = config.ProviderConfig{Type: "rss", SearchURL: srv.URL + "?apikey=super-secret&q={query}"}
	a.screen = screenSources
	if strings.Contains(a.View(), "super-secret") {
		t.Fatal("source screen printed credentials")
	}
	selectSource(a, "local")
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if cmd == nil || !a.sources.checking["local"] {
		t.Fatal("disabled source could not be checked")
	}
	if _, duplicate := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}); duplicate != nil {
		t.Fatal("duplicate check started")
	}
	a.Update(cmd())
	s := sources.List(a.cfg)[len(sources.List(a.cfg))-1]
	if a.cfg.Providers["local"].Enabled || !strings.HasPrefix(a.sourceStatus(s), "blocked · retry in") {
		t.Fatal("failed check enabled source or hid block")
	}
	status.Store(http.StatusOK)
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	a.Update(cmd())
	if a.cfg.Providers["local"].Enabled || !strings.Contains(a.sourceStatus(s), "working · 0 results") {
		t.Fatal("zero-result check was not treated as a response")
	}
	// Arbitrary parser errors can quote URLs. Only known, safe classifications
	// can enter the view or the stored session reading.
	a.sources.checking["local"] = true
	a.Update(sourceCheckedMsg{generation: a.sources.checks["local"], id: "local", reading: sourceReading{probe: health.ProviderProbe{Err: "https://example.org/?apikey=super-secret"}}})
	if strings.Contains(a.View(), "super-secret") || a.sourceStatus(s) != "check failed" {
		t.Fatal("raw check error leaked")
	}
	a.sources.saving = "local"
	a.Update(sourceSavedMsg{id: "local", enabled: true, err: errors.New("disk full")})
	if a.cfg.Providers["local"].Enabled {
		t.Fatal("failed save changed config")
	}
}

func TestAllSourcesDisabledKeepsOldResultsUsable(t *testing.T) {
	a := sourcesTestApp(t)
	old := a.agg
	a.nextAgg = a.agg.WithProviders(nil)
	a.results.query = "previous search"
	a.startSearch("new query")
	if a.agg != old || a.results.query != "previous search" || !strings.Contains(a.errText, "open sources") {
		t.Fatal("empty selection discarded old results or lacked a recovery action")
	}
}

func TestSourcesSmallWindowsAndInvalidConfig(t *testing.T) {
	a := sourcesTestApp(t)
	a.screen = screenSources
	p := a.cfg.Providers["eztv"]
	p.Mirror = "file:///tmp/private"
	a.cfg.Providers["eztv"] = p
	selectSource(a, "eztv")
	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeySpace}); cmd == nil || a.sources.saving != "" {
		t.Fatal("invalid source was allowed to save")
	}
	for _, width := range []int{8, 20, 40, 65, 100} {
		a.width, a.height = width, 14
		view := a.View()
		assertRenderFits(t, view, width)
		if len(strings.Split(view, "\n")) != 14 {
			t.Fatal("sources exceeded terminal height")
		}
	}
}

func TestSourceCheckTimeoutAndStrictProxy(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer srv.Close()
		a := sourcesTestApp(t)
		a.cfg.SearchTimeoutSeconds = 1
		a.cfg.Providers["local"] = config.ProviderConfig{Type: "rss", SearchURL: srv.URL}
		a.screen = screenSources
		selectSource(a, "local")
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
		start := time.Now()
		a.Update(cmd())
		if time.Since(start) > 3*time.Second || a.sources.checking["local"] || a.sources.readings["local"].probe.Err != "timed out" {
			t.Fatal("check did not finish at configured deadline")
		}
	})
	t.Run("strict proxy", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.Write([]byte("<rss><channel/></rss>")) }))
		defer srv.Close()
		dir := t.TempDir()
		// Diagnostic loads retain invalid proxy state and must fail closed. A source
		// test must use that same client, including for a normally local feed.
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("proxy:\n  socks5: http://invalid-proxy\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := config.LoadReadOnlyFrom(dir)
		if err != nil {
			t.Fatal(err)
		}
		a := sourcesTestApp(t)
		a.cfg = cfg
		a.cfg.Providers["local"] = config.ProviderConfig{Type: "rss", SearchURL: srv.URL}
		a.screen = screenSources
		selectSource(a, "local")
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
		a.Update(cmd())
		if calls.Load() != 0 || a.sources.readings["local"].probe.OK {
			t.Fatal("source test bypassed strict proxy")
		}
	})
}
