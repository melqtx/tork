package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var allScreens = []screen{
	screenSearch, screenISOs, screenResults, screenPreview, screenDownloads, screenHealth,
}

// The footer shares its line with the right-aligned proxy badge, so a strip
// that merely fits the column still collides with it. This is the regression
// that let the downloads footer grow to twelve hints, run past the content
// column, and overwrite the badge. Narrow terminals are the hard case, so the
// strip is checked from a comfortable 120 columns down to a cramped 60.
func TestKeyStripAlwaysFitsItsBudget(t *testing.T) {
	for _, width := range []int{120, 100, 80, 60} {
		a := &App{width: width, height: 40, proxy: proxyBadge{state: proxyBadgeUnverified}}
		for _, s := range allScreens {
			a.screen = s
			budget := a.helpBudget(a.contentWidth())
			strip := a.keyStrip(budget)
			if got := lipgloss.Width(strip); got > budget {
				t.Errorf("%dc %s: strip is %d cells over a budget of %d",
					width, a.screenTitle(), got, budget)
			}
			// Trimming must never cost the pointer to the full list.
			if !strings.Contains(strip, "keys") {
				t.Errorf("%dc %s: strip dropped the ? pointer: %q", width, a.screenTitle(), strip)
			}
		}
	}
}

// Every screen must put something on the strip, and the card must be a superset
// of it: the strip is a preview of the card, not a separate list.
// A short terminal is the interesting case: the card used to run off the
// bottom of a 20-line window, quietly dropping `^c quit` from the only place it
// is documented.
func TestEveryScreenDocumentsItsKeys(t *testing.T) {
	for _, height := range []int{40, 24, 20} {
		a := &App{width: 100, height: height}
		for _, s := range allScreens {
			a.screen = s
			keys := a.screenKeys()
			if len(keys) == 0 {
				t.Errorf("%s documents no keys at all", a.screenTitle())
				continue
			}
			onStrip := 0
			for _, kh := range keys {
				if kh.strip {
					onStrip++
				}
			}
			if onStrip == 0 {
				t.Errorf("%s has an empty footer strip", a.screenTitle())
			}
			lines := a.helpLines()
			var left, right []string
			columnWidth := (a.contentWidth() - 4) / 2
			for _, line := range lines {
				left = append(left, ansi.Cut(line, 0, columnWidth))
				right = append(right, ansi.Cut(line, columnWidth+4, a.contentWidth()))
			}
			card := strings.Join(left, " ") + " " + strings.Join(right, " ")
			for _, kh := range append(append([]keyHint{}, keys...), globalKeys...) {
				if !strings.Contains(strings.Join(strings.Fields(card), " "), strings.Join(strings.Fields(kh.label), " ")) {
					t.Errorf("%dr %s: card omits %q (%s)", height, a.screenTitle(), kh.label, kh.key)
				}
			}
		}
	}
}

// ^d is the one shortcut that must survive a focused text field: it is a chord
// precisely so it can work while a query or a filter is being typed, which is
// where the single-letter jumps have to stay inert.
func TestCtrlDJumpsToDownloadsFromAnywhere(t *testing.T) {
	for _, s := range allScreens {
		a := &App{width: 100, height: 40, screen: s}
		a.search.input.SetValue("still typing this")
		a.results.filtering = true
		_, _ = a.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

		if s == screenPreview {
			if a.screen != screenPreview {
				t.Errorf("^d left the preview modal for %v; it should be inert there", a.screen)
			}
			continue
		}
		if a.screen != screenDownloads {
			t.Errorf("^d on %v landed on %v, want downloads", s, a.screen)
		}
	}
}

// The card is a reference, not a mode, so ^d should act and put it away rather
// than being swallowed as "any key closes it".
func TestCtrlDActsThroughTheHelpCard(t *testing.T) {
	a := &App{width: 100, height: 40, screen: screenResults, showHelp: true}
	_, _ = a.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if a.showHelp {
		t.Error("^d left the key card open")
	}
	if a.screen != screenDownloads {
		t.Errorf("^d through the card landed on %v, want downloads", a.screen)
	}
}

func TestHelpCardOpensAndAnyKeyCloses(t *testing.T) {
	a := &App{width: 100, height: 40, screen: screenDownloads}
	if _, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}); !a.showHelp {
		t.Fatal("? did not open the key card")
	}
	if _, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}); a.showHelp {
		t.Fatal("the key card survived a keypress; it should close on anything")
	}

	// Typing a query must still produce a literal question mark.
	a.screen = screenSearch
	a.search.input.SetValue("what is this")
	if _, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}); a.showHelp {
		t.Fatal("? opened the card while a search was being typed")
	}
	a.search.input.SetValue("")
	if _, _ = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}); !a.showHelp {
		t.Fatal("? did not open the card from an empty home field")
	}
}
