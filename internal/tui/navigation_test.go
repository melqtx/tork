package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/melqtx/tork/internal/aggregator"
)

func navigationApp() *App {
	return &App{width: 100, height: 30, search: newSearchModel(), results: newTestResults(), downloads: newDownloadsModel(), agg: aggregator.New(nil, 0, 0)}
}

func TestResultsEmptyStatesStayActionableInBothViews(t *testing.T) {
	a := navigationApp()
	a.results.query = ""
	a.screen = screenResults
	for _, grouped := range []bool{false, true} {
		a.results.grouped = grouped
		view := a.View()
		if !strings.Contains(view, "No search yet") || !strings.Contains(view, "esc") {
			t.Fatal("empty results need a search action")
		}
	}
}

func TestHomeEnterUsesVisibleFocus(t *testing.T) {
	a := navigationApp()
	a.focusSearch()
	a.search.input.SetValue("saved query")
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	if !a.search.menuFocused || a.search.input.Focused() {
		t.Fatal("menu and field disagree about focus")
	}
	if !strings.Contains(a.viewSearch(), "enter open downloads") {
		t.Fatal("missing focused action cue")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if a.screen != screenDownloads {
		t.Fatal("Enter submitted query instead of opening downloads")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.screen != screenSearch || a.search.menuFocused || !a.search.input.Focused() || a.search.input.Value() != "saved query" {
		t.Fatal("returning home did not preserve query and restore field focus")
	}
}

func TestHomeTypingAndEscapeRestoreFocus(t *testing.T) {
	a := navigationApp()
	a.focusSearch()
	a.search.input.SetValue("linux")
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	if a.search.menuFocused || a.search.input.Value() != "linux!" {
		t.Fatal("typing from menu failed to edit query")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.search.menuFocused || a.search.input.Value() != "linux!" {
		t.Fatal("first Escape should restore focus without clearing")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.screen != screenSearch || a.search.input.Value() != "" {
		t.Fatal("Escape should clear and stay home")
	}
}

func TestTabMovesHomeFocusAndDownloadsReturnsToResults(t *testing.T) {
	a := navigationApp()
	a.focusSearch()
	for i := 0; i < len(a.homeDestinations()); i++ {
		a.Update(tea.KeyMsg{Type: tea.KeyTab})
		if a.screen != screenSearch || !a.search.menuFocused || a.search.menu != i {
			t.Fatal("Tab must focus home controls, not leave home")
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if a.search.menuFocused || !a.search.input.Focused() {
		t.Fatal("Tab should wrap to search")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if a.search.menu != len(a.homeDestinations())-1 {
		t.Fatal("reverse Tab should focus last control")
	}
	a.screen = screenResults
	a.results.filterIn.SetValue("res:1080p")
	for _, back := range []tea.KeyType{tea.KeyTab, tea.KeyEsc} {
		a.Update(tea.KeyMsg{Type: tea.KeyTab})
		if a.screen != screenDownloads {
			t.Fatal("Tab should show downloads")
		}
		a.Update(tea.KeyMsg{Type: back})
		if a.screen != screenResults || a.results.filterIn.Value() != "res:1080p" {
			t.Fatal("back should preserve results")
		}
	}
}

func TestNavigationCannotEscapeDownloadDialogs(t *testing.T) {
	for _, prompt := range []bool{true, false} {
		a := navigationApp()
		a.screen = screenDownloads
		if prompt {
			a.downloads.prompt = newPathPrompt(pathActionMove, downloadItem{}, "folder", "")
		} else {
			a.downloads.confirmRemove = &removeConfirm{}
		}
		for _, key := range []tea.KeyType{tea.KeyTab, tea.KeyShiftTab, tea.KeyF1, tea.KeyF2, tea.KeyF4, tea.KeyCtrlD} {
			a.Update(tea.KeyMsg{Type: key})
			if a.screen != screenDownloads {
				t.Fatal("navigation abandoned pending dialog")
			}
		}
		a.Update(tea.KeyMsg{Type: tea.KeyEsc})
		a.Update(tea.KeyMsg{Type: tea.KeyTab})
		if a.screen != screenSearch {
			t.Fatal("navigation stayed locked after cancelling dialog")
		}
	}
}

func TestFunctionKeysDoNotNavigateOrClutterHome(t *testing.T) {
	a := navigationApp()
	for _, key := range []tea.KeyType{tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4} {
		a.Update(tea.KeyMsg{Type: key})
		if a.screen != screenSearch {
			t.Fatal("function key unexpectedly navigated")
		}
	}
	if strings.Contains(a.View(), "F1") || strings.Contains(a.View(), "F2") {
		t.Fatal("function-key chrome remains")
	}
	a.results.query = ""
	if len(a.homeDestinations()) != 2 {
		t.Fatal("empty results should not clutter home")
	}
}

func TestNavigationAndHomeFitSmallWindows(t *testing.T) {
	for _, width := range []int{20, 40, 60, 80, 100} {
		a := navigationApp()
		a.width = width
		a.height = 14
		assertRenderFits(t, a.View(), width)
		if len(strings.Split(a.View(), "\n")) != a.height {
			t.Fatal("home exceeds terminal height")
		}
		if !strings.Contains(a.View(), "linux isos") {
			t.Fatalf("home menu clipped at width %d", width)
		}

	}
}

func TestHelpScrollReachesLastShortcut(t *testing.T) {
	a := navigationApp()
	a.width = 40
	a.height = 14
	a.screen = screenDownloads
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	for i := 0; i < 20; i++ {
		a.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if !a.showHelp || !strings.Contains(a.View(), "quit") {
		t.Fatal("last help shortcut unreachable")
	}
	assertRenderFits(t, a.View(), a.width)
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.showHelp || a.screen != screenDownloads {
		t.Fatal("Escape should only close help")
	}
}
