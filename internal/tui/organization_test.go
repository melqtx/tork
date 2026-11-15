package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/state"
)

func TestOrganizationToggleSavesFromEmptyDownloads(t *testing.T) {
	t.Parallel()
	cfg, err := config.LoadReadOnlyFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil, nil, &state.State{}, nil)
	a.screen, a.width, a.height = screenDownloads, 100, 30
	if !strings.Contains(a.viewDownloads(), "folders: on") {
		t.Fatal("empty downloads hides the folder toggle")
	}
	for _, enabled := range []bool{false, true} {
		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}})
		if cmd == nil || !a.downloads.savingOrganization {
			t.Fatal("toggle did not schedule a save")
		}
		if cfg.Organization.Enabled == enabled {
			t.Fatal("toggle changed before saving")
		}
		if _, duplicate := a.updateDownloads(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'O'}}); duplicate != nil {
			t.Fatal("repeated key queued overlapping saves")
		}
		msg := cmd()
		if _, ok := msg.(organizationSavedMsg); !ok {
			t.Fatalf("toggle returned %T", msg)
		}
		a.Update(msg)
		if cfg.Organization.Enabled != enabled || a.downloads.savingOrganization {
			t.Fatal("saved setting was not applied")
		}
		reloaded, err := config.LoadReadOnlyFrom(cfg.Dir())
		if err != nil || reloaded.Organization.Enabled != enabled {
			t.Fatalf("toggle did not survive reload: %v", err)
		}
	}
	for _, width := range []int{32, 60, 100} {
		a.width = width
		assertRenderFits(t, a.viewDownloads(), width)
	}
}

func TestOrganizationToggleFailureKeepsPreviousSetting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := config.LoadReadOnlyFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("organization: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(cfg, nil, nil, &state.State{}, nil)
	cmd := a.toggleOrganization()
	a.Update(cmd())
	if !cfg.Organization.Enabled || a.downloads.savingOrganization {
		t.Fatal("failed save changed the organization setting")
	}
	if !strings.Contains(a.errText, "could not save folder organization") {
		t.Fatalf("failed save was not shown: %q", a.errText)
	}
}
