package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
)

func TestDownloadPathKeepsCategoryAndFilename(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Downloads", "tork")
	path := filepath.Join(root, "movies", "日本語 film.mkv")
	a := &App{cfg: &config.Config{DownloadDir: root}}
	if got := a.downloadDisplayPath(path, 80); got != "tork/movies/日本語 film.mkv" {
		t.Fatalf("display path = %q", got)
	}
	for _, width := range []int{12, 24, 40} {
		got := a.downloadDisplayPath(path, width)
		if lipgloss.Width(got) > width || !strings.HasSuffix(got, "film.mkv") {
			t.Fatalf("path at %d cells = %q", width, got)
		}
	}
	outside := filepath.Join(t.TempDir(), "archive", "keep.mkv")
	if got := a.downloadDisplayPath(outside, 500); got != filepath.ToSlash(outside) {
		t.Fatalf("external path was displayed as inside the root: %q", got)
	}
	if got := a.downloadDisplayPath(path, 0); got != "" {
		t.Fatalf("zero-width display = %q", got)
	}
	a.height = 32
	card := a.downloadDetail(downloadItem{Name: strings.Repeat("long name ", 20), DataPath: path, State: engine.StateDone}, 100)
	if lines := len(strings.Split(card, "\n")); lines != 6 {
		t.Fatalf("detail card wrapped into %d lines: %s", lines, card)
	}
}
