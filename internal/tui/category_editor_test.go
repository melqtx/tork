package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/state"
)

func TestCategoryEditorSavesAndSelectsNewCategory(t *testing.T) {
	cfg := config.Default(t.TempDir())
	a := New(cfg, nil, nil, &state.State{}, nil)
	a.screen, a.width, a.height = screenPreview, 100, 30
	a.preview.owned = true
	a.openPreviewCategories()
	a.openCategoryEditor()
	e := a.preview.categories.editor
	e.inputs[0].SetValue("Courses")
	e.inputs[1].SetValue("Learning")
	_, save := a.updateCategoryEditor(tea.KeyMsg{Type: tea.KeyEnter})
	if save == nil || !e.saving || !a.typing() {
		t.Fatal("editor did not enter its saving state")
	}
	_, apply := a.Update(save())
	if apply == nil || a.preview.categories.editor != nil || cfg.Organization.Folders["Courses"] != "Learning" {
		t.Fatal("saved category was not selected for the preview")
	}
	reloaded, err := config.LoadReadOnlyFrom(cfg.Dir())
	if err != nil || reloaded.Organization.Folders["Courses"] != "Learning" {
		t.Fatalf("category did not persist: %v", err)
	}
}

func TestCategoryEditorCancelKeepsMenu(t *testing.T) {
	a := New(config.Default(t.TempDir()), nil, nil, &state.State{}, nil)
	a.preview.owned = true
	a.openPreviewCategories()
	a.openCategoryEditor()
	a.updateCategoryEditor(tea.KeyMsg{Type: tea.KeyEsc})
	if a.preview.categories == nil || a.preview.categories.editor != nil {
		t.Fatal("cancel did not return to category selection")
	}
}
