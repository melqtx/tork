package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/config"
)

type categoryEditor struct {
	inputs [2]textinput.Model
	focus  int
	saving bool
}

type categorySavedMsg struct {
	category config.Category
	folder   string
	err      error
}

func (a *App) openCategoryEditor() tea.Cmd {
	e := &categoryEditor{}
	for i := range e.inputs {
		e.inputs[i] = textinput.New()
		e.inputs[i].Prompt = "❯ "
		e.inputs[i].CharLimit = 240
	}
	e.inputs[0].CharLimit = 64
	e.inputs[0].Placeholder = "e.g. Courses"
	e.inputs[1].Placeholder = "same as category name"
	a.preview.categories.editor = e
	return e.inputs[0].Focus()
}

func (a *App) updateCategoryEditor(msg tea.Msg) (tea.Model, tea.Cmd) {
	e := a.preview.categories.editor
	if e.saving {
		return a, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			a.preview.categories.editor = nil
			return a, nil
		case "tab", "shift+tab":
			e.inputs[e.focus].Blur()
			e.focus = 1 - e.focus
			return a, e.inputs[e.focus].Focus()
		case "enter":
			e.saving = true
			name, folder := strings.TrimSpace(e.inputs[0].Value()), strings.TrimSpace(e.inputs[1].Value())
			if folder == "" {
				folder = name
			}
			cfg := *a.cfg
			return a, func() tea.Msg {
				category, err := cfg.WriteCategory(name, folder)
				return categorySavedMsg{category: category, folder: folder, err: err}
			}
		}
	}
	var cmd tea.Cmd
	e.inputs[e.focus], cmd = e.inputs[e.focus].Update(msg)
	return a, cmd
}

func (a *App) onCategorySaved(msg categorySavedMsg) tea.Cmd {
	m := a.preview.categories
	if m == nil || m.editor == nil {
		return nil
	}
	m.editor.saving = false
	if msg.err != nil {
		return a.showError(msg.err.Error())
	}
	if a.cfg.Organization.Folders == nil {
		a.cfg.Organization.Folders = map[string]string{}
	}
	a.cfg.Organization.Folders[msg.category.ID] = msg.folder
	m.options = a.cfg.Organization.Categories()
	for i, option := range m.options {
		if option.ID == msg.category.ID {
			m.win.cursor = i
		}
	}
	m.editor = nil
	_, cmd := a.updatePreviewCategories(tea.KeyMsg{Type: tea.KeyEnter})
	return cmd
}

func (a *App) viewCategoryEditor(e *categoryEditor) string {
	body := strings.Join([]string{
		styleTitle.Render("New category"), "",
		"Name", e.inputs[0].View(), "",
		"Folder", e.inputs[1].View(), "",
		styleDim.Render("Saved inside your download folder."),
	}, "\n")
	return a.chrome("categories", body,
		hints(hint("tab", "next field"), hint("enter", "add & use"), hint("esc", "back")))
}
