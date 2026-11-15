package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/engine"
	"github.com/melqtx/tork/internal/state"
)

func TestOrganizeExistingRequiresApprovalAndPreservesBytes(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.DownloadDir = t.TempDir()
	old := t.TempDir()
	st := &state.State{}
	for i := range 2 {
		name := fmt.Sprintf("payload-%d.txt", i)
		path := filepath.Join(old, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		st.Upsert(state.Entry{Magnet: name, Name: name, Category: "books", CategorySource: "manual",
			Paused: i == 1, Done: i == 0, DownloadDir: old, DataPath: path, Seed: state.Bool(false)})
	}
	a := New(cfg, &engine.Engine{}, nil, st, nil)
	a.screen = screenDownloads
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.downloads.organizationPlan != nil || st.Entries[0].DownloadDir != old {
		t.Fatal("cancelled preview changed downloads")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	p := a.downloads.organizationPlan
	for i, category := range p.menu.options {
		if category.ID == "movies" {
			p.menu.win.cursor = i
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if st.Entries[0].Category != "books" {
		t.Fatal("category preview changed saved state")
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for a.downloads.organizationPlan != nil {
		if cmd == nil {
			t.Fatal("bulk move stopped without completing")
		}
		_, cmd = a.Update(cmd())
	}
	for i, e := range st.Entries {
		category := "books"
		if i == 0 {
			category = "movies"
		}
		want := filepath.Join(cfg.DownloadDir, category, e.Name)
		if e.DataPath != want || e.Category != category || e.Paused != (i == 1) || e.Done != (i == 0) || e.PendingMove != nil {
			t.Fatalf("move changed saved settings: %+v", e)
		}
		if data, err := os.ReadFile(want); err != nil || string(data) != e.Name {
			t.Fatalf("move corrupted bytes: %v", err)
		}
	}
	if strings.Contains(a.viewDownloads(), "checking files") {
		t.Fatal("successful move left completed files unchecked")
	}

}
