package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/melqtx/tork/internal/config"
	"github.com/melqtx/tork/internal/health"
)

// compassApp builds an App whose health store already holds two daily
// snapshots: knaben healthy throughout, nyaa down throughout, one swarm
// growing and one starving.
func compassApp(t *testing.T) *App {
	t.Helper()
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := health.Open(filepath.Join(t.TempDir(), "health.json"))
	for i, seeds := range []int{4, 9} {
		snap := health.Snapshot{
			At:   time.Now().Add(time.Duration(i-1) * 24 * time.Hour),
			Kind: health.KindDaily,
			Providers: []health.ProviderProbe{
				{Name: "knaben", OK: true, LatencyMS: 120, Results: 50},
				{Name: "nyaa", Err: "timed out"},
			},
			Swarms: []health.SwarmProbe{
				{Hash: "a", Name: "growing movie", Seeders: seeds, Peers: 20},
				{Hash: "b", Name: "starving movie", Seeders: 0, Peers: 1},
			},
		}
		if err := store.Append(snap); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{cfg: cfg, health: store, width: 100, height: 30}
	a.refreshCompass()
	return a
}

func TestCompassRendersFleetAndLibrary(t *testing.T) {
	a := compassApp(t)
	a.screen = screenHealth
	out := a.viewHealth()

	for _, want := range []string{
		"sources", "library",
		"knaben", "120ms", "50 results",
		"nyaa", "timed out",
		"growing movie", "starving movie",
		"1/2 sources up", // one of two providers answering
		"1 dying",        // the starving, unfinished download
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compass missing %q in:\n%s", want, out)
		}
	}
}

func TestCompassTrends(t *testing.T) {
	a := compassApp(t)
	if len(a.compass.providers) != 2 {
		t.Fatalf("got %d provider trends, want 2", len(a.compass.providers))
	}
	knaben, nyaa := a.compass.providers[0], a.compass.providers[1]
	if knaben.Streak != 2 {
		t.Errorf("knaben streak = %d, want 2", knaben.Streak)
	}
	if nyaa.Streak != -2 {
		t.Errorf("nyaa streak = %d, want -2", nyaa.Streak)
	}

	growing, starving := a.compass.swarms[0], a.compass.swarms[1]
	if growing.Delta != 5 {
		t.Errorf("growing swarm delta = %d, want +5 (4 -> 9)", growing.Delta)
	}
	if growing.Dying {
		t.Error("a growing swarm must not be dying")
	}
	if !starving.Dying {
		t.Error("an unfinished swarm with no seeders across both checks must be dying")
	}
}

// An empty history renders a calm prompt rather than a broken screen.
func TestCompassEmptyHistory(t *testing.T) {
	cfg, err := config.LoadFrom(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: cfg, health: health.Open(filepath.Join(t.TempDir(), "h.json")), width: 100, height: 30}
	a.refreshCompass()
	a.screen = screenHealth
	out := a.viewHealth()
	if !strings.Contains(out, "no health check recorded yet") {
		t.Errorf("empty compass missing its status line:\n%s", out)
	}
	if !strings.Contains(out, "press r to run one") {
		t.Errorf("empty compass should invite a manual check:\n%s", out)
	}
}

// H opens the compass from a list screen and esc returns to exactly where it
// was pressed.
func TestHealthKeyOpensAndEscReturns(t *testing.T) {
	a := compassApp(t)
	a.screen = screenDownloads

	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")}); cmd != nil {
		t.Fatal("opening the compass should not need a command")
	}
	if a.screen != screenHealth {
		t.Fatalf("screen = %v after H, want screenHealth", a.screen)
	}

	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.screen != screenDownloads {
		t.Fatalf("screen = %v after esc, want the screen H was pressed on", a.screen)
	}
}

// H is a letter: it must stay inert while a text field owns the keyboard.
func TestHealthKeyInertWhileTyping(t *testing.T) {
	a := compassApp(t)
	a.screen = screenSearch
	a.search = newSearchModel()
	a.search.input.Focus() // the real app focuses the field in Init

	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if a.screen == screenHealth {
		t.Fatal("H opened the compass while typing in the search box")
	}
	if got := a.search.input.Value(); got != "H" {
		t.Fatalf("search box holds %q, want the typed H", got)
	}
}

func TestHealthKeyInertInDownloadPathPrompt(t *testing.T) {
	a := compassApp(t)
	a.screen = screenDownloads
	a.downloads.prompt = newPathPrompt(pathActionMove, downloadItem{}, "new folder: ", "")
	a.downloads.prompt.input.Focus()
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if a.screen == screenHealth {
		t.Fatal("H opened the compass while a download path prompt owned input")
	}
	if got := a.downloads.prompt.input.Value(); got != "H" {
		t.Fatalf("path prompt holds %q, want typed H", got)
	}
}

// Section headings are labels, not choices: the cursor must never rest on one,
// whether opening the screen, walking down through the "library" heading, or
// jumping to either end.
func TestCompassCursorSkipsHeadings(t *testing.T) {
	a := compassApp(t)
	a.screen = screenDownloads
	a.openHealth()

	body := a.compassBody()
	isHeading := func(i int) bool { return body[i].heading != "" }

	if isHeading(a.compass.win.cursor) {
		t.Fatalf("compass opened with the cursor on heading %q", body[a.compass.win.cursor].heading)
	}
	for step := 0; step < len(body)+2; step++ {
		a.Update(tea.KeyMsg{Type: tea.KeyDown})
		if isHeading(a.compass.win.cursor) {
			t.Fatalf("cursor landed on heading %q walking down", body[a.compass.win.cursor].heading)
		}
	}
	for step := 0; step < len(body)+2; step++ {
		a.Update(tea.KeyMsg{Type: tea.KeyUp})
		if isHeading(a.compass.win.cursor) {
			t.Fatalf("cursor landed on heading %q walking up", body[a.compass.win.cursor].heading)
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if isHeading(a.compass.win.cursor) {
		t.Fatal("cursor landed on a heading after jumping to the end")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if isHeading(a.compass.win.cursor) {
		t.Fatal("cursor landed on a heading after jumping to the top")
	}
}

// The preview modal is a focused decision; the compass must not steal it.
func TestHealthKeyInertInPreview(t *testing.T) {
	a := compassApp(t)
	a.screen = screenPreview
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
	if a.screen == screenHealth {
		t.Fatal("H opened the compass from the preview modal")
	}
}
