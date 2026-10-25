package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/provider"
	"github.com/melqtx/tork/internal/sources"
)

func TestAddSourceOpensReadyToUseCatalog(t *testing.T) {
	a := sourcesTestApp(t)
	a.width, a.height, a.screen = 100, 25, screenSources
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if a.sources.catalog == nil || a.sources.editor != nil {
		t.Fatal("add still opened an endpoint form")
	}
	view := a.View()
	for _, label := range []string{"The Pirate Bay", "TheRARBG", "LimeTorrents"} {
		if !strings.Contains(view, label) {
			t.Fatalf("missing ready-to-use source %s", label)
		}
	}
	if strings.Contains(view, "API key") || strings.Contains(view, "Endpoint URL") {
		t.Fatal("normal source selection requires technical configuration")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Lime")})
	list := a.catalogSources()
	if len(list) != 1 || list[0].ID != "limetorrents" {
		t.Fatalf("source-name filtering failed: %+v", list)
	}
	old := a.agg
	_, save := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if save == nil || a.cfg.Providers["limetorrents"].Enabled {
		t.Fatal("catalog bypassed save or did not start adding")
	}
	_, check := a.Update(save())
	// Do not execute this network check in a unit test; its captured provider and
	// bounded command are exercised separately by the opt-in live test.
	if check == nil || !a.sources.checking["limetorrents"] || !a.cfg.Providers["limetorrents"].Enabled || a.sources.catalog == nil || a.agg != old {
		t.Fatal("add did not save, keep the picker open, preserve current results and schedule a check")
	}
	loaded, err := config.LoadReadOnlyFrom(filepath.Dir(a.cfg.ConfigPath()))
	if err != nil || !loaded.Providers["limetorrents"].Enabled {
		t.Fatalf("source choice did not survive restart: %v", err)
	}
	for _, s := range sources.Available(a.cfg) {
		if s.ID == "limetorrents" {
			t.Fatal("enabled source is offered for adding again")
		}
	}
}

func TestSourceCatalogCancelAdvancedAndFailedSave(t *testing.T) {
	a := sourcesTestApp(t)
	a.screen = screenSources
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if a.screen != screenSources {
		t.Fatal("downloads abandoned source selection")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.sources.catalog != nil || a.sources.editor != nil {
		t.Fatal("cancel left source selection open")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if err := os.Remove(a.cfg.ConfigPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(a.cfg.ConfigPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	first := a.catalogSources()[0].ID
	_, save := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a.Update(save())
	if a.cfg.Providers[first].Enabled || a.sources.catalog == nil || !strings.Contains(a.errText, "not saved") {
		t.Fatal("failed save enabled source or lost selection")
	}
	a.sources.catalog.win.cursor = len(a.catalogSources())
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.sources.editor == nil || a.sources.catalog != nil {
		t.Fatal("advanced feed configuration is unreachable")
	}
}

func TestSourceCatalogFitsNarrowWindows(t *testing.T) {
	a := sourcesTestApp(t)
	a.screen = screenSources
	a.openSourceCatalog()
	for _, width := range []int{8, 20, 40, 80, 120} {
		for _, height := range []int{10, 14, 30} {
			a.width, a.height = width, height
			for i := 0; i < len(a.catalogSources())+1; i++ {
				a.sources.catalog.win.cursor = i
				view := a.View()
				assertRenderFits(t, view, width)
				if len(strings.Split(view, "\n")) != height {
					t.Fatal("source picker exceeded terminal height")
				}
			}
		}
	}
}

func TestAddedLimeTorrentsSourceSearchesAndResolvesThroughTheApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/") {
			w.Write([]byte(`<table class="table2"><tr><td><div class="tt-name"><a href="/Open-game-torrent-1.html">Open game Linux</a></div></td><td>today - in Games</td><td>1 GB</td><td class="tdseed">12</td><td class="tdleech">1</td></tr></table>`))
		} else {
			w.Write([]byte(`<a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567">Magnet</a>`))
		}
	}))
	defer srv.Close()
	a := sourcesTestApp(t)
	for id, p := range a.cfg.Providers {
		p.Enabled = false
		a.cfg.Providers[id] = p
	}
	p := a.cfg.Providers["limetorrents"]
	p.Mirror = srv.URL
	a.cfg.Providers["limetorrents"] = p
	a.screen = screenSources
	a.openSourceCatalog()
	a.sources.catalog.filter.SetValue("LimeTorrents")
	_, save := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a.Update(save())
	a.startSearch("open game")
	for r := range a.results.resultCh {
		a.Update(resultMsg{searchID: a.results.searchID, r: r})
	}
	for range a.results.statusCh {
	}
	if len(a.results.rows) != 1 || a.results.rows[0].res.Category != "Games" {
		t.Fatal("adding the direct source did not produce broad search results in the app")
	}
	resolver := a.findResolver("limetorrents")
	if resolver == nil {
		t.Fatal("selected result lost its resolver")
	}
	magnet, err := resolver.ResolveMagnet(context.Background(), a.results.rows[0].res)
	if err != nil || (provider.Result{Magnet: magnet}).InfoHash() == "" {
		t.Fatalf("selected result is not usable: %v", err)
	}
}

func TestCatalogAddsSeveralSourcesWithoutReopening(t *testing.T) {
	a := sourcesTestApp(t)
	a.screen = screenSources
	a.openSourceCatalog()
	for _, name := range []string{"LimeTorrents", "TheRARBG"} {
		a.sources.catalog.filter.SetValue(name)
		id := a.catalogSources()[0].ID
		_, save := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
		a.Update(save())
		if a.sources.catalog == nil || a.sources.catalog.filter.Value() != "" || !a.cfg.Providers[id].Enabled || !a.sources.checking[id] {
			t.Fatal("adding source did not prepare picker for another addition")
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.sources.catalog != nil || a.screen != screenSources {
		t.Fatal("done must return to source statuses")
	}
}
