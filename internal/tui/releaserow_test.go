package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
)

func TestReadableReleaseTitlePreservesIdentity(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"Dune.2021.1080p.WEB-DL.x265-GROUP", "Dune 2021"},
		{"Example.S02E03.2160p.BluRay.x265.mkv", "Example S02E03"},
		{"日本語の映画 (2024) 1080p WEB-DL", "日本語の映画 (2024)"},
		{"Ubuntu.24.04.1.iso", "Ubuntu 24.04.1 iso"},
		{"(500) Days of Summer 2009 1080p", "(500) Days of Summer 2009"},
		{"1080p", "1080p"},
	} {
		if got := readableReleaseTitle(tt.raw); got != tt.want {
			t.Errorf("%q => %q want %q", tt.raw, got, tt.want)
		}
	}
}
func TestReleaseRowSeparatesFactsAndKeepsOriginalDetails(t *testing.T) {
	a := picksApp()
	raw := "Dune.2021.1080p.WEB-DL.x265.HDR-GROUP"
	addPick(a, 1, raw, 24, 3<<30)
	row := a.results.rows[0]
	line := renderReleaseRow(row, 99)
	for _, want := range []string{"Dune 2021", "WEB-DL", "x265", "HDR", "3.0 GiB", "24 seeds"} {
		if !strings.Contains(line, want) {
			t.Errorf("row missing %q", want)
		}
	}
	if strings.Contains(line, "GROUP") {
		t.Fatal("release suffix still crowds the row")
	}
	if !strings.Contains(a.View(), raw) {
		t.Fatal("full original title missing from details")
	}
	for _, w := range []int{20, 40, 60, 80, 100} {
		assertRenderFits(t, renderReleaseRow(row, w), w)
	}
}
func previewSummaryApp() *App {
	files := []engine.FileInfo{{Index: 7, Path: "Release/movie.mkv", Length: 3 << 30}, {Index: 19, Path: "Release/info.nfo", Length: 100}, {Index: 23, Path: "Release/sample.mp4", Length: 1000}}
	excluded := junkFiles(files)
	p := previewModel{name: "Dune.2021.1080p.WEB-DL.x265", ready: true, files: files, excluded: excluded, autoSkipped: len(excluded), tree: buildPreviewTree(files, excluded)}
	p.rebuildRows()
	p.rebuildStats()
	return &App{screen: screenPreview, width: 100, height: 24, cfg: &config.Config{DownloadDir: "/downloads/tork"}, preview: p}
}
func TestPreviewSummaryAndExtrasSelection(t *testing.T) {
	a := previewSummaryApp()
	view := a.View()
	for _, want := range []string{"Selected 3.0 GiB", "1 of 3 files", "Save to", "/downloads/tork", "enter download", "Extras"} {
		if !strings.Contains(view, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	if strings.Contains(view, "info.nfo") || strings.Contains(view, "sample.mp4") {
		t.Fatal("extras are not collapsed")
	}
	var extras *fileNode
	for _, n := range a.preview.rows {
		if n.name == "Extras" {
			extras = n
		}
	}
	if extras == nil || !extras.collapsed {
		t.Fatal("missing collapsed extras group")
	}
	a.preview.toggleNode(extras)
	if a.preview.selectedFiles() != 3 || a.preview.selectedBytes() != (3<<30)+1100 {
		t.Fatal("selecting collapsed extras failed to include original indices")
	}
	extras.collapsed = false
	a.preview.rebuildRows()
	if !strings.Contains(a.View(), "info.nfo") {
		t.Fatal("expanded extras not accessible")
	}
	a.preview.saveDir = "/existing/download/location"
	if !strings.Contains(a.View(), a.preview.saveDir) {
		t.Fatal("preview should show actual engine destination")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if !strings.Contains(a.View(), "select a file to download") {
		t.Fatal("empty selection still suggests download")
	}
	if a.preview.files[1].Path != "Release/info.nfo" {
		t.Fatal("display grouping changed original file path")
	}
}
func TestPreviewSummaryFitsShortAndNarrowWindows(t *testing.T) {
	for _, w := range []int{20, 40, 60, 100} {
		a := previewSummaryApp()
		a.width = w
		a.height = 14
		view := a.View()
		assertRenderFits(t, view, w)
		if len(strings.Split(view, "\n")) != a.height {
			t.Fatal("preview overflows screen height")
		}
		for _, want := range []string{"Selected", "Save to", "enter download", "Extras"} {
			if !strings.Contains(view, want) {
				t.Errorf("width %d missing %q", w, want)
			}
		}
	}
}
