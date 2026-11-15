package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/state"
)

func TestOrganizationPreviewDoesNotMoveOrRecategorize(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DownloadDir = filepath.Join(t.TempDir(), "tork")
	old := filepath.Join(t.TempDir(), "payload.txt")
	if err := os.WriteFile(old, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &state.State{Entries: []state.Entry{{Magnet: "saved", Name: "payload.txt", Category: "books",
		CategorySource: "manual", Paused: true, DownloadDir: filepath.Dir(old), DataPath: old}}}
	a := New(cfg, nil, nil, st, nil)
	plan := a.buildOrganizationPlan()
	if len(plan.moves) != 1 || plan.moves[0].destination.DownloadDir != filepath.Join(cfg.DownloadDir, "books") {
		t.Fatalf("unexpected plan: %+v", plan.moves)
	}
	plan.moves[0].choose(cfg, "movies", "manual")
	for _, width := range []int{32, 80, 120} {
		a.width, a.height = width, 24
		assertRenderFits(t, a.viewOrganizationPlan(plan), width)
	}
	if st.Entries[0].Category != "books" {
		t.Fatal("preview changed the saved category")
	}
	if data, err := os.ReadFile(old); err != nil || string(data) != "original" {
		t.Fatalf("preview touched the payload: %v", err)
	}
	if _, err := os.Stat(cfg.DownloadDir); !os.IsNotExist(err) {
		t.Fatal("preview created destination folders")
	}
}
