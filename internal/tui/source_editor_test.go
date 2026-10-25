package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/health"
)

func prepareSourceDraft(a *App, name, endpoint string) {
	a.screen = screenSources
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	a.sources.catalog.win.cursor = len(a.catalogSources())
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	a.sources.editor.inputs[0].SetValue(name)
	a.sources.editor.inputs[1].SetValue(endpoint)
}

func TestCustomSourceAddCheckSaveSearchEditRemove(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "secret-key" || r.URL.Query().Get("t") != "search" {
			t.Error("source did not send Torznab authentication/search parameters")
		}
		w.Write([]byte(`<rss xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><item><title>Ubuntu 1080p</title><torznab:attr name="infohash" value="0123456789abcdef0123456789abcdef01234567"/></item></channel></rss>`))
	}))
	defer srv.Close()
	a := sourcesTestApp(t)
	// Only the local custom source may run in this test.
	for id, p := range a.cfg.Providers {
		p.Enabled = false
		a.cfg.Providers[id] = p
	}
	prepareSourceDraft(a, "My Linux", srv.URL+"?apikey=secret-key")
	for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlD}, {Type: tea.KeyRunes, Runes: []rune("H")}} {
		// Keep this on the type control so a literal H doesn't edit the name.
		a.sources.editor.focus = 1
		a.Update(key)
		if a.screen != screenSources || a.sources.editor == nil {
			t.Fatal("global shortcut abandoned editor")
		}
	}
	_, check := a.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if check == nil {
		t.Fatalf("check missing: %s", a.errText)
	}
	if strings.Contains(a.View(), "secret-key") {
		t.Fatal("copied API key was not moved to masked field")
	}
	a.Update(check())
	if a.sources.editor.reading == nil || !a.sources.editor.reading.probe.OK {
		t.Fatal("draft check failed")
	}
	_, save := a.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if save == nil {
		t.Fatalf("save missing: %s", a.errText)
	}
	oldAgg := a.agg
	a.Update(save())
	if a.sources.editor != nil || !a.cfg.Providers["my-linux"].Enabled || a.agg != oldAgg {
		t.Fatal("save failed or replaced current results' providers")
	}
	reloaded, err := config.LoadReadOnlyFrom(filepath.Dir(a.cfg.ConfigPath()))
	if err != nil || reloaded.Providers["my-linux"].APIKey != "secret-key" || reloaded.Providers["my-linux"].Label != "My Linux" {
		t.Fatalf("reload failed: %v", err)
	}
	info, err := os.Stat(a.cfg.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("credentials were saved with public permissions")
	}
	a.startSearch("ubuntu")
	for r := range a.results.resultCh {
		a.Update(resultMsg{searchID: a.results.searchID, r: r})
	}
	for range a.results.statusCh {
	}
	if len(a.results.rows) != 1 || a.results.rows[0].res.Provider != "my-linux" {
		t.Fatal("saved source did not participate in the next search")
	}
	a.navigate(screenSources)
	selectSource(a, "my-linux")
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	a.sources.editor.inputs[0].SetValue("Renamed Linux")
	_, check = a.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	a.Update(check())
	_, save = a.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	a.Update(save())
	if a.cfg.Providers["my-linux"].Label != "Renamed Linux" {
		t.Fatal("editing failed to retain the source ID and update its label")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if a.sources.editor.id != "my-linux" {
		t.Fatal("edit lost stable source identity")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := a.cfg.Providers["my-linux"]; !ok {
		t.Fatal("cancelling removal removed source")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	_, remove := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	a.Update(remove())
	if _, ok := a.cfg.Providers["my-linux"]; ok {
		t.Fatal("confirmed removal kept source")
	}
	if len(a.results.rows) != 1 || len(a.agg.Providers()) != 1 {
		t.Fatal("removing a source discarded existing search results/providers")
	}
	reloaded, err = config.LoadReadOnlyFrom(filepath.Dir(a.cfg.ConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.Providers["my-linux"]; ok {
		t.Fatal("removal did not survive reload")
	}
}

func TestCustomDraftBadCheckSavesDisabledAndIgnoresLateChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`<html><body>login</body></html>`)) }))
	defer srv.Close()
	a := sourcesTestApp(t)
	prepareSourceDraft(a, "Broken feed", srv.URL)
	_, check := a.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	a.Update(check())
	if a.sources.editor.reading.probe.OK || !strings.Contains(a.View(), "not an RSS/Torznab feed") {
		t.Fatal("HTML was reported as a healthy empty feed")
	}
	_, save := a.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	a.Update(save())
	if a.cfg.Providers["broken-feed"].Enabled {
		t.Fatal("bad check enabled source")
	}
	prepareSourceDraft(a, "Another feed", srv.URL)
	generation := a.sources.editor.generation
	a.sources.editor.focus = 0
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	a.Update(sourceDraftCheckedMsg{generation: generation, reading: sourceReading{probe: health.ProviderProbe{OK: true}, at: time.Now()}})
	if a.sources.editor.reading != nil {
		t.Fatal("stale check validated an edited draft")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(sourceDraftCheckedMsg{generation: generation, reading: sourceReading{probe: health.ProviderProbe{OK: true}}})
	if a.sources.editor != nil {
		t.Fatal("late check reopened closed form")
	}
}

func TestSourceEditorFitsSmallWindowsAndKeepsKeysMasked(t *testing.T) {
	a := sourcesTestApp(t)
	prepareSourceDraft(a, "Linux", "https://example.org/api")
	a.sources.editor.inputs[2].SetValue("never-display-this-key")
	for _, width := range []int{8, 20, 40, 80, 120} {
		for _, height := range []int{10, 14, 30} {
			for focus := 0; focus < 4; focus++ {
				a.width, a.height, a.sources.editor.focus = width, height, focus
				view := a.View()
				assertRenderFits(t, view, width)
				if strings.Contains(view, "never-display-this-key") || len(strings.Split(view, "\n")) != height {
					t.Fatal("editor leaked key or exceeded terminal height")
				}
			}
		}
	}
}

func TestEditingDraftCancelsItsNetworkCheck(t *testing.T) {
	entered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer srv.Close()
	a := sourcesTestApp(t)
	prepareSourceDraft(a, "Cancelable", srv.URL)
	_, check := a.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	done := make(chan tea.Msg, 1)
	go func() { done <- check() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	a.sources.editor.focus = 0
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	select {
	case msg := <-done:
		a.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("editing did not cancel network check")
	}
	if a.sources.editor.checking || a.sources.editor.reading != nil {
		t.Fatal("cancelled check changed edited draft status")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := a.cfg.Providers["cancelable"]; ok {
		t.Fatal("cancelling draft saved a source")
	}
}

func TestCustomSourceSaveChecksFirstAndOnlyAutoSavesSuccess(t *testing.T) {
	for _, healthy := range []bool{true, false} {
		t.Run(map[bool]string{true: "healthy", false: "failed"}[healthy], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if healthy {
					w.Write([]byte(`<rss><channel></channel></rss>`))
				} else {
					w.WriteHeader(http.StatusUnauthorized)
				}
			}))
			defer srv.Close()
			a := sourcesTestApp(t)
			prepareSourceDraft(a, "Quick feed", srv.URL)
			_, check := a.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
			if check == nil || !a.sources.editor.checking || a.sources.saving != "" {
				t.Fatal("save must check before writing")
			}
			_, save := a.Update(check())
			if !healthy {
				if save != nil || a.sources.editor == nil {
					t.Fatal("failed check must leave draft open without saving")
				}
				_, save = a.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
			}
			if save == nil {
				t.Fatal("missing save command")
			}
			a.Update(save())
			p, ok := a.cfg.Providers["quick-feed"]
			if !ok || p.Enabled != healthy || a.sources.editor != nil {
				t.Fatal("incorrect saved source state")
			}
		})
	}
}

func TestEditingCancelsPendingAutomaticSourceSave(t *testing.T) {
	a := sourcesTestApp(t)
	prepareSourceDraft(a, "Quick feed", "https://example.org/api")
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	generation := a.sources.editor.generation
	a.sources.editor.focus = 0
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	_, cmd := a.Update(sourceDraftCheckedMsg{generation: generation, reading: sourceReading{probe: health.ProviderProbe{OK: true}}})
	if cmd != nil || a.sources.saving != "" || a.sources.editor.saveAfterCheck {
		t.Fatal("stale response saved edited draft")
	}
}
